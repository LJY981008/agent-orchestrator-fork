package ladologwatch

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// certsMaxFile caps each imported file.
const certsMaxFile = 64 << 10

// Errors CertStore.Import gives. The messages wrapped around them never name
// a file path: paths come from the request only and do not go back out.
var (
	// ErrCertInvalid is a file that is not what it should be: unreadable,
	// too large, not PEM, not a certificate, or a certificate and key that do
	// not pair.
	ErrCertInvalid = errors.New("the TLS file is not valid")
	// ErrCertKeyEncrypted is a private key with a passphrase: the daemon
	// cannot unlock it.
	ErrCertKeyEncrypted = errors.New("the TLS private key is encrypted")
)

// certsNamePattern is a stored set's directory name.
var certsNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// File names inside a stored set's directory.
const (
	certsCAFile   = "ca.pem"
	certsCertFile = "cert.pem"
	certsKeyFile  = "key.pem"
)

// CertStore keeps LADO's own copies of the TLS files of Docker sources under
// <StateDir>/lado/credentials/docker/<name>/ (ADR 0012): ca.pem, and cert.pem
// with key.pem when the source has a client certificate. Directories are
// 0700 and files 0600; an import replaces a set whole. The user's original
// files are only read.
type CertStore struct {
	dir string
	mu  sync.Mutex
}

// NewCertStore returns the store under AO's state directory.
func NewCertStore(stateDir string) *CertStore {
	return &CertStore{dir: filepath.Join(stateDir, "lado", "credentials", "docker")}
}

// CertName is the directory name of a source's set: the first 16 hex digits
// of sha256(project, source).
func CertName(project, source string) string {
	sum := sha256.Sum256([]byte(project + "\x00" + source))
	return hex.EncodeToString(sum[:])[:16]
}

// Import checks the files at the given paths and copies them in as the set
// name. The CA is needed; the client certificate and key come both or
// neither. A failure leaves the stored set as it was.
func (c *CertStore) Import(name, ca, cert, key string) error {
	if !certsNamePattern.MatchString(name) {
		return fmt.Errorf("%w: bad certificate set name", ErrCertInvalid)
	}
	ca, cert, key = strings.TrimSpace(ca), strings.TrimSpace(cert), strings.TrimSpace(key)
	if ca == "" {
		return fmt.Errorf("%w: a CA certificate is needed", ErrCertInvalid)
	}
	if (cert == "") != (key == "") {
		return fmt.Errorf("%w: give the client certificate and its key together", ErrCertInvalid)
	}
	caPEM, err := certsRead(ca, "CA certificate")
	if err != nil {
		return err
	}
	if err := certsCheckCertificates(caPEM, "CA certificate"); err != nil {
		return err
	}
	files := map[string][]byte{certsCAFile: caPEM}
	if cert != "" {
		certPEM, err := certsRead(cert, "client certificate")
		if err != nil {
			return err
		}
		keyPEM, err := certsRead(key, "private key")
		if err != nil {
			return err
		}
		if err := certsCheckCertificates(certPEM, "client certificate"); err != nil {
			return err
		}
		if err := certsCheckKey(keyPEM); err != nil {
			return err
		}
		if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
			return fmt.Errorf("%w: the client certificate and the private key do not pair", ErrCertInvalid)
		}
		files[certsCertFile] = certPEM
		files[certsKeyFile] = keyPEM
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.replace(name, files)
}

// Delete removes the set name. A missing set is not an error.
func (c *CertStore) Delete(name string) error {
	if !certsNamePattern.MatchString(name) {
		return fmt.Errorf("%w: bad certificate set name", ErrCertInvalid)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.RemoveAll(filepath.Join(c.dir, name)); err != nil {
		return fmt.Errorf("remove the stored TLS files: %w", err)
	}
	return nil
}

// Paths gives the stored files of the set name; ok is whether its CA is
// stored. cert and key are empty when there is no client certificate.
func (c *CertStore) Paths(name string) (ca, cert, key string, ok bool) {
	hasCA, hasCert, hasKey := c.Stored(name)
	if !hasCA {
		return "", "", "", false
	}
	dir := filepath.Join(c.dir, name)
	ca = filepath.Join(dir, certsCAFile)
	if hasCert && hasKey {
		cert = filepath.Join(dir, certsCertFile)
		key = filepath.Join(dir, certsKeyFile)
	}
	return ca, cert, key, true
}

// Stored reports which files of the set name are stored.
func (c *CertStore) Stored(name string) (ca, cert, key bool) {
	if !certsNamePattern.MatchString(name) {
		return false, false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	dir := filepath.Join(c.dir, name)
	return certsIsFile(filepath.Join(dir, certsCAFile)),
		certsIsFile(filepath.Join(dir, certsCertFile)),
		certsIsFile(filepath.Join(dir, certsKeyFile))
}

// replace writes files into a new directory next to the set and swaps it in.
// The caller holds c.mu.
func (c *CertStore) replace(name string, files map[string][]byte) error {
	if err := certsMkdir(filepath.Dir(c.dir)); err != nil {
		return err
	}
	if err := certsMkdir(c.dir); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(c.dir, "."+name+"-new-")
	if err != nil {
		return fmt.Errorf("store the TLS files: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := os.Chmod(tmp, 0o700); err != nil { //nolint:gosec // G302: a directory needs its execute bit; 0700 keeps it to the user.
		return fmt.Errorf("protect the TLS files: %w", err)
	}
	for file, data := range files {
		if err := certsWriteFile(filepath.Join(tmp, file), data); err != nil {
			return err
		}
	}
	final := filepath.Join(c.dir, name)
	old := ""
	if _, err := os.Lstat(final); err == nil {
		old = filepath.Join(c.dir, "."+name+"-old")
		_ = os.RemoveAll(old)
		if err := os.Rename(final, old); err != nil {
			return fmt.Errorf("replace the stored TLS files: %w", err)
		}
	}
	if err := os.Rename(tmp, final); err != nil {
		if old != "" {
			_ = os.Rename(old, final)
		}
		return fmt.Errorf("replace the stored TLS files: %w", err)
	}
	if old != "" {
		_ = os.RemoveAll(old)
	}
	return nil
}

// certsMkdir makes a 0700 directory.
func certsMkdir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the LADO credentials directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a directory needs its execute bit; 0700 keeps it to the user.
		return fmt.Errorf("protect the LADO credentials directory: %w", err)
	}
	return nil
}

// certsWriteFile writes a 0600 file and syncs it.
func certsWriteFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: the path is inside the LADO credentials directory.
	if err != nil {
		return fmt.Errorf("store the TLS files: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("store the TLS files: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("store the TLS files: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("store the TLS files: %w", err)
	}
	return nil
}

// certsIsFile reports whether path is a regular file.
func certsIsFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

// certsRead reads an imported file of at most certsMaxFile bytes. Errors
// name what the file is for, never its path.
func certsRead(path, what string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the user chose this file to import; it is only read.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: the %s file does not exist", ErrCertInvalid, what)
		}
		return nil, fmt.Errorf("%w: the %s file cannot be read", ErrCertInvalid, what)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: the %s is not a regular file", ErrCertInvalid, what)
	}
	data, err := io.ReadAll(io.LimitReader(f, certsMaxFile+1))
	if err != nil {
		return nil, fmt.Errorf("%w: the %s file cannot be read", ErrCertInvalid, what)
	}
	if len(data) > certsMaxFile {
		return nil, fmt.Errorf("%w: the %s file is larger than 64 KiB", ErrCertInvalid, what)
	}
	return data, nil
}

// certsCheckCertificates checks that data holds PEM CERTIFICATE blocks only,
// at least one, each a valid x509 certificate.
func certsCheckCertificates(data []byte, what string) error {
	count := 0
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return fmt.Errorf("%w: the %s file holds a %q block, not only certificates", ErrCertInvalid, what, block.Type)
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return fmt.Errorf("%w: the %s file holds a certificate that does not parse", ErrCertInvalid, what)
		}
		count++
	}
	if count == 0 {
		return fmt.Errorf("%w: the %s file is not a PEM certificate", ErrCertInvalid, what)
	}
	return nil
}

// certsCheckKey checks that data holds one unencrypted PEM private key.
func certsCheckKey(data []byte) error {
	rest := data
	found := false
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "ENCRYPTED PRIVATE KEY" || strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") {
			return ErrCertKeyEncrypted
		}
		if strings.HasSuffix(block.Type, "PRIVATE KEY") {
			found = true
		}
	}
	if !found {
		if bytes.Contains(data, []byte("ENCRYPTED")) {
			return ErrCertKeyEncrypted
		}
		return fmt.Errorf("%w: the private key file is not a PEM private key", ErrCertInvalid)
	}
	return nil
}

// dockerTLSConfig is the TLS of a Docker source: the CA copy as the only
// root, the client certificate when there is one, TLS 1.2 or later, and the
// endpoint's host as the name to verify. It never skips verification.
func dockerTLSConfig(caFile, certFile, keyFile, serverName string) (*tls.Config, error) {
	if caFile == "" {
		return nil, errors.New("docker TLS needs a CA certificate; import one first")
	}
	caPEM, err := os.ReadFile(caFile) //nolint:gosec // G304: the path is the LADO copy in the credentials directory.
	if err != nil {
		return nil, errors.New("docker TLS: the stored CA certificate cannot be read")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("docker TLS: the stored CA certificate is not valid")
	}
	config := &tls.Config{
		RootCAs:    roots,
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
	}
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return nil, errors.New("docker TLS: the client certificate needs its key")
		}
		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, errors.New("docker TLS: the stored client certificate cannot be loaded")
		}
		config.Certificates = []tls.Certificate{pair}
	}
	return config, nil
}
