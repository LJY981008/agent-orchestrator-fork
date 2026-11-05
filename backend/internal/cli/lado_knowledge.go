package cli

// LADO fork: `ao lado knowledge write`, how a LADO session adds or updates a
// note in its project's knowledge store (#24). The session never touches the
// store's folder: it pipes the note in and the daemon writes it, picking the
// project from the session and checking --base against the note's revision.

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// ladoKnowledgeSlugPattern mirrors the daemon's slug rule: the note's file
// name, in characters every OS accepts.
var ladoKnowledgeSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// writeLadoKnowledgeNoteRequest mirrors controllers.WriteLadoKnowledgeNoteRequest.
type writeLadoKnowledgeNoteRequest struct {
	SessionID    string `json:"sessionId"`
	Content      string `json:"content"`
	BaseRevision *int   `json:"baseRevision,omitempty"`
}

// ladoKnowledgeNoteResponse is the part of the daemon's note summary the CLI
// prints.
type ladoKnowledgeNoteResponse struct {
	Slug     string `json:"slug"`
	Revision int    `json:"revision"`
}

func newLadoKnowledgeCommand(ctx *commandContext) *cobra.Command {
	knowledge := &cobra.Command{
		Use:   "knowledge",
		Short: "Write to this project's LADO knowledge store",
	}
	knowledge.AddCommand(newLadoKnowledgeWriteCommand(ctx))
	return knowledge
}

func newLadoKnowledgeWriteCommand(ctx *commandContext) *cobra.Command {
	var slug string
	var base int
	cmd := &cobra.Command{
		Use:   "write",
		Short: "Add or update a knowledge note, read from stdin, as this session",
		Long: "Add or update a note in this project's LADO knowledge store. Pipe the whole note in: " +
			"YAML front matter, then the body. Leave --base out for a new note; to update one, pass the " +
			"revision you read. If the note changed since, LADO refuses with LADO_KNOWLEDGE_CHANGED and " +
			"its current revision: read it again, merge, and send it with that revision.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			slug = strings.TrimSpace(slug)
			if !ladoKnowledgeSlugPattern.MatchString(slug) {
				return usageError{fmt.Errorf("usage: --note must be a slug of lowercase letters, digits and hyphens, at most 64, starting with a letter or digit (got %q)", slug)}
			}
			var baseRevision *int
			if cmd.Flags().Changed("base") {
				if base < 1 {
					return usageError{fmt.Errorf("usage: --base must be a revision of 1 or more (got %d)", base)}
				}
				baseRevision = &base
			}
			session := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
			if !sessionIDPattern.MatchString(session) {
				return usageError{errors.New("usage: run this inside a LADO session (AO_SESSION_ID is not set)")}
			}
			content, err := readLadoSubmission(cmd)
			if err != nil {
				return err
			}
			var res ladoKnowledgeNoteResponse
			path := "lado/knowledge/notes/" + url.PathEscape(slug)
			body := writeLadoKnowledgeNoteRequest{SessionID: session, Content: string(content), BaseRevision: baseRevision}
			// A refusal shows the daemon's envelope as it is: for
			// LADO_KNOWLEDGE_CHANGED its message names the current revision.
			if err := ctx.putJSON(cmd.Context(), path, body, &res); err != nil {
				return err
			}
			if res.Slug == "" {
				res.Slug = slug
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "LADO saved %s as revision %d.\n", res.Slug, res.Revision)
			return err
		},
	}
	cmd.Flags().StringVar(&slug, "note", "", "The note's slug, its file name without .md (required)")
	cmd.Flags().IntVar(&base, "base", 0, "The revision the update starts from; leave it out for a new note")
	return cmd
}
