package ladologwatch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The log stream of GET /containers/{id}/logs comes in one of two shapes
// (ADR 0012):
//
//   - multiplexed (a container without a TTY): frames of an 8-byte header
//     [stream, 0, 0, 0, size as uint32 big-endian] and size bytes of payload.
//     Stream 1 is stdout and 2 is stderr; 0 (stdin) and 3 (the Engine's own
//     error) are not log lines and only end up in the stream's detail.
//   - raw (a container with a TTY): the bytes as the container wrote them.
//
// An Engine with API 1.42 or later says which in the Content-Type. An older
// Engine says raw-stream for both, so without the multiplexed Content-Type
// the TTY flag of the container's inspect decides, and a stream of a
// container without a TTY is read as multiplexed only when its first byte
// can start a frame header (with timestamps=1 a raw line starts with a
// digit). A line that crosses frames is joined in a buffer per stream; a
// line is cut at logsMaxLine and the rest up to its newline dropped; a frame
// is read in pieces of at most logsChunk, so a frame of any size takes
// bounded memory.

var _ LogSource = (*DockerSource)(nil)

const (
	// logsMaxLine is the longest log line kept; the rest of a longer line is
	// dropped.
	logsMaxLine = 64 << 10
	// logsChunk is the largest piece of a frame read at once, so a frame of
	// any size (a 1MiB frame and more) takes bounded memory.
	logsChunk = 32 << 10
	// logsMaxDetail caps the stream detail kept from stdin and system-error
	// frames.
	logsMaxDetail = 300

	logsContentMultiplexed = "application/vnd.docker.multiplexed-stream"
	logsContentRaw         = "application/vnd.docker.raw-stream"

	logsStreamStdout = "stdout"
	logsStreamStderr = "stderr"
)

// logsStreamError is a log stream that ended with the Engine's own error
// (a system-error frame) or that cannot be read as a log stream.
type logsStreamError struct {
	Detail string
}

func (e *logsStreamError) Error() string {
	return "docker log stream: " + e.Detail
}

// Follow reads the logs of one instance from a point on and calls onLine for
// each line, with the timestamp the Engine put in front of it, until ctx ends
// or the Engine ends the stream (the container stopped, say). With
// from.TailZero, or without from.Since, it asks for tail=0 so that only new
// lines come; otherwise it asks for the lines since from.Since, which the
// Engine includes, so the caller skips lines at or before its cursor. A
// stream that ends is nil; ctx ending is ctx's error; a container that is
// gone is a *dockerStatusError with status 404 (dockerNotFound).
func (s *DockerSource) Follow(ctx context.Context, inst Instance, from FollowFrom, onLine func(stream string, ts time.Time, text string)) error {
	if !dockerIDPattern.MatchString(inst.ID) {
		return fmt.Errorf("docker container id %q is not hex", inst.ID)
	}
	resp, err := s.dockerGet(ctx, "/containers/"+inst.ID+"/logs", logsQuery(from))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	err = logsRead(resp.Body, resp.Header.Get("Content-Type"), inst.Tty, onLine)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// logsQuery is the query of a follow from a point.
func logsQuery(from FollowFrom) url.Values {
	q := url.Values{
		"follow":     {"1"},
		"stdout":     {"1"},
		"stderr":     {"1"},
		"timestamps": {"1"},
	}
	if from.TailZero || from.Since.IsZero() || from.Since.Unix() < 0 {
		q.Set("tail", "0")
		return q
	}
	q.Set("since", logsSince(from.Since))
	return q
}

// logsSince is a time as the Engine's since: seconds.nanoseconds.
func logsSince(t time.Time) string {
	return strconv.FormatInt(t.Unix(), 10) + "." + fmt.Sprintf("%09d", t.Nanosecond())
}

// logsRead splits a log stream into lines and hands each to onLine. It
// returns nil at the stream's end, the read error otherwise.
func logsRead(body io.Reader, contentType string, tty bool, onLine func(stream string, ts time.Time, text string)) error {
	r := bufio.NewReaderSize(body, 64<<10)
	multiplexed, err := logsMultiplexed(r, contentType, tty)
	if err != nil {
		return logsEnd(err)
	}
	p := &logsParser{onLine: onLine}
	if multiplexed {
		err = p.readFrames(r)
	} else {
		err = p.readRaw(r)
	}
	p.flush()
	if err := logsEnd(err); err != nil {
		return err
	}
	if p.detail != "" {
		return &logsStreamError{Detail: p.detail}
	}
	return nil
}

// logsEnd turns the end of a stream into nil.
func logsEnd(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// logsMultiplexed decides the shape of a stream: multiplexed when the
// Content-Type says so; raw for a TTY container; otherwise (an older Engine
// says raw-stream for both shapes, and a missing Content-Type says nothing)
// multiplexed when the first byte can start a frame header. It waits for the
// first byte only in that last case.
func logsMultiplexed(r *bufio.Reader, contentType string, tty bool) (bool, error) {
	media, _, _ := mime.ParseMediaType(contentType)
	switch {
	case media == logsContentMultiplexed:
		return true, nil
	case tty:
		return false, nil
	}
	first, err := r.Peek(1)
	if err != nil {
		return false, err
	}
	return first[0] <= 3, nil
}

// logsParser keeps the partial line of each stream and the stream's detail.
type logsParser struct {
	onLine func(stream string, ts time.Time, text string)
	stdout logsLine
	stderr logsLine
	// lastTS is the timestamp of the last line that had one; a line without
	// one gets it.
	lastTS time.Time
	detail string
}

// logsLine is the partial line of one stream.
type logsLine struct {
	buf []byte
	// dropping is true after a line was cut, until its newline.
	dropping bool
}

// readFrames reads multiplexed frames until the stream ends.
func (p *logsParser) readFrames(r io.Reader) error {
	var header [8]byte
	chunk := make([]byte, logsChunk)
	for {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return &logsStreamError{Detail: "stream cut inside a frame header"}
			}
			return err
		}
		kind := header[0]
		if kind > 3 || header[1] != 0 || header[2] != 0 || header[3] != 0 {
			return &logsStreamError{Detail: "not a multiplexed log stream"}
		}
		size := int64(binary.BigEndian.Uint32(header[4:]))
		var line *logsLine
		stream := ""
		switch kind {
		case 1:
			line, stream = &p.stdout, logsStreamStdout
		case 2:
			line, stream = &p.stderr, logsStreamStderr
		}
		for size > 0 {
			n := min(size, int64(len(chunk)))
			read, err := io.ReadFull(r, chunk[:n])
			size -= int64(read)
			if line != nil {
				p.write(line, stream, chunk[:read])
			} else {
				p.addDetail(chunk[:read])
			}
			if err != nil {
				if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
					return &logsStreamError{Detail: "stream cut inside a frame"}
				}
				return err
			}
		}
	}
}

// readRaw reads a raw stream until it ends; every line is stdout.
func (p *logsParser) readRaw(r io.Reader) error {
	chunk := make([]byte, logsChunk)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			p.write(&p.stdout, logsStreamStdout, chunk[:n])
		}
		if err != nil {
			return err
		}
	}
}

// write adds a piece of a stream and hands on every line it completes.
func (p *logsParser) write(line *logsLine, stream string, data []byte) {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		piece := data
		if i >= 0 {
			piece = data[:i]
		}
		if !line.dropping {
			room := logsMaxLine - len(line.buf)
			if len(piece) > room {
				line.buf = append(line.buf, piece[:room]...)
				p.emit(line, stream)
				line.dropping = true
			} else {
				line.buf = append(line.buf, piece...)
			}
		}
		if i < 0 {
			return
		}
		if line.dropping {
			line.dropping = false
		} else {
			p.emit(line, stream)
		}
		data = data[i+1:]
	}
}

// flush hands on the partial lines at the stream's end.
func (p *logsParser) flush() {
	if len(p.stdout.buf) > 0 {
		p.emit(&p.stdout, logsStreamStdout)
	}
	if len(p.stderr.buf) > 0 {
		p.emit(&p.stderr, logsStreamStderr)
	}
}

// emit hands on a stream's buffered line and empties the buffer.
func (p *logsParser) emit(line *logsLine, stream string) {
	raw := string(line.buf)
	line.buf = line.buf[:0]
	raw = strings.TrimSuffix(raw, "\r")
	if len(raw) >= logsMaxLine {
		raw = logsTrimRune(raw)
	}
	ts, text, ok := logsTimestamp(raw)
	if ok {
		p.lastTS = ts
	} else {
		ts = p.lastTS
	}
	if p.onLine != nil {
		p.onLine(stream, ts, text)
	}
}

// addDetail keeps the text of stdin and system-error frames, clipped.
func (p *logsParser) addDetail(data []byte) {
	room := logsMaxDetail - len(p.detail)
	if room <= 0 {
		return
	}
	text := strings.TrimSpace(strings.ToValidUTF8(string(data[:min(len(data), room+4)]), ""))
	if text == "" {
		return
	}
	if p.detail != "" {
		text = " " + text
	}
	p.detail = dockerClip(p.detail+text, logsMaxDetail)
}

// logsTimestamp splits the RFC3339Nano timestamp the Engine puts in front of
// a line with timestamps=1 from the line's text.
func logsTimestamp(line string) (time.Time, string, bool) {
	prefix, rest, found := strings.Cut(line, " ")
	if !found {
		prefix, rest = line, ""
	}
	if len(prefix) < len("2006-01-02T15:04:05Z") || prefix[0] < '0' || prefix[0] > '9' {
		return time.Time{}, line, false
	}
	ts, err := time.Parse(time.RFC3339Nano, prefix)
	if err != nil {
		return time.Time{}, line, false
	}
	return ts, rest, true
}

// logsTrimRune drops a rune a cut left incomplete at the end of s.
func logsTrimRune(s string) string {
	for i := 0; i < utf8.UTFMax-1 && s != ""; i++ {
		r, size := utf8.DecodeLastRuneInString(s)
		if r != utf8.RuneError || size != 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}
