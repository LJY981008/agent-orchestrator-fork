package cli

// LADO fork: `ao lado timings`, which prints how long a task's stages took and
// what its sessions spent on investigation, or a project's baseline over its
// newest tasks (issue #34).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// ladoTimingPoint mirrors controllers.LadoTimingPointResponse.
type ladoTimingPoint struct {
	Stage       string    `json:"stage"`
	At          time.Time `json:"at"`
	SincePrevMs int64     `json:"sincePrevMs"`
	Estimated   bool      `json:"estimated"`
}

// ladoTaskTimeline mirrors controllers.LadoTaskTimelineResponse.
type ladoTaskTimeline struct {
	Points       []ladoTimingPoint `json:"points"`
	TotalMs      int64             `json:"totalMs"`
	PlanRequests int               `json:"planRequests"`
	Revisions    int               `json:"revisions"`
	Approvals    int               `json:"approvals"`
	Runs         int               `json:"runs"`
}

// ladoSessionMetrics mirrors controllers.LadoSessionMetricsResponse.
type ladoSessionMetrics struct {
	SessionID          string `json:"sessionId"`
	Role               string `json:"role"`
	ToolCalls          int    `json:"toolCalls"`
	ReadCalls          int    `json:"readCalls"`
	SearchCalls        int    `json:"searchCalls"`
	CommandCalls       int    `json:"commandCalls"`
	EditCalls          int    `json:"editCalls"`
	InvestigationCalls int    `json:"investigationCalls"`
	InvestigateMs      int64  `json:"investigateMs"`
	Stored             bool   `json:"stored"`
}

// ladoTaskTimings mirrors controllers.LadoTaskTimingsResponse.
type ladoTaskTimings struct {
	TaskID   string               `json:"taskId"`
	IssueRef string               `json:"issueRef"`
	Title    string               `json:"title"`
	Timeline ladoTaskTimeline     `json:"timeline"`
	Sessions []ladoSessionMetrics `json:"sessions"`
}

// ladoTimingsSummary mirrors controllers.LadoTimingsSummaryResponse.
type ladoTimingsSummary struct {
	ProjectID string            `json:"projectId"`
	Tasks     []ladoTaskTimings `json:"tasks"`
	Gaps      []struct {
		From     string `json:"from"`
		To       string `json:"to"`
		Count    int    `json:"count"`
		MedianMs int64  `json:"medianMs"`
		MaxMs    int64  `json:"maxMs"`
	} `json:"gaps"`
	Investigation []struct {
		Role                     string `json:"role"`
		Sessions                 int    `json:"sessions"`
		MedianCalls              int    `json:"medianCalls"`
		MedianInvestigationCalls int    `json:"medianInvestigationCalls"`
		MedianInvestigateMs      int64  `json:"medianInvestigateMs"`
	} `json:"investigation"`
}

const maxLadoTimingsLimit = 100

func newLadoTimingsCommand(ctx *commandContext) *cobra.Command {
	var task, project string
	var limit int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "timings",
		Short: "Show how long a task's stages took and its sessions' investigation, or a project's baseline",
		Long: "Show how long a LADO task took from intake to merge, stage by stage, and what each of its plan and " +
			"execution sessions spent on investigation (--task), or a project's baseline over its newest tasks: " +
			"the median and longest time between stages and each role's median investigation (--project). " +
			"An estimated time was inferred for a run before LADO recorded it; an estimated verified time is an upper bound.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			task, project = strings.TrimSpace(task), strings.TrimSpace(project)
			if (task == "") == (project == "") {
				return usageError{errors.New("usage: pass exactly one of --task or --project")}
			}
			limited := cmd.Flags().Changed("limit")
			if limited && task != "" {
				return usageError{errors.New("usage: --limit goes with --project")}
			}
			if limited && (limit < 1 || limit > maxLadoTimingsLimit) {
				return usageError{fmt.Errorf("usage: --limit must be from 1 to %d", maxLadoTimingsLimit)}
			}
			path := "lado/tasks/" + url.PathEscape(task) + "/timings"
			if project != "" {
				query := url.Values{"projectId": {project}}
				if limited {
					query.Set("limit", strconv.Itoa(limit))
				}
				path = "lado/timings?" + query.Encode()
			}
			var raw json.RawMessage
			if err := ctx.getJSON(cmd.Context(), path, &raw); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), raw)
			}
			if task != "" {
				var res ladoTaskTimings
				if err := json.Unmarshal(raw, &res); err != nil {
					return fmt.Errorf("decode the timings: %w", err)
				}
				return writeLadoTaskTimings(cmd.OutOrStdout(), res)
			}
			var res ladoTimingsSummary
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("decode the timings: %w", err)
			}
			return writeLadoTimingsSummary(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().StringVar(&task, "task", "", "The LADO task to show")
	cmd.Flags().StringVar(&project, "project", "", "The project whose baseline to show")
	cmd.Flags().IntVar(&limit, "limit", 20, "How many of the project's newest tasks to sum up, 1 to 100")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the daemon's answer as JSON")
	return cmd
}

func writeLadoTaskTimings(out io.Writer, res ladoTaskTimings) error {
	tl := res.Timeline
	total := "not merged yet"
	if tl.TotalMs > 0 {
		total = ladoMillis(tl.TotalMs)
	}
	if _, err := fmt.Fprintf(out, "%s  %s  %s\nPlan requests %d, revisions %d, approvals %d, runs %d. Total: %s\n\n",
		res.TaskID, ladoOrDash(res.IssueRef), res.Title, tl.PlanRequests, tl.Revisions, tl.Approvals, tl.Runs, total); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if len(tl.Points) == 0 {
		if _, err := fmt.Fprintln(out, "No stages recorded."); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(tw, "STAGE\tAT\tSINCE PREVIOUS"); err != nil {
			return err
		}
		for i, p := range tl.Points {
			since := "-"
			if i > 0 {
				since = ladoMillis(p.SincePrevMs)
			}
			at := p.At.UTC().Format(time.RFC3339)
			if p.Estimated {
				at += " (estimated)"
			}
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Stage, at, since); err != nil {
				return err
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	if len(res.Sessions) == 0 {
		_, err := fmt.Fprintln(out, "No sessions recorded.")
		return err
	}
	if _, err := fmt.Fprintln(tw, "SESSION\tROLE\tTOOL CALLS\tREAD\tSEARCH\tCOMMAND\tEDIT\tINVESTIGATE\tSOURCE"); err != nil {
		return err
	}
	for _, s := range res.Sessions {
		source := "stored"
		if !s.Stored {
			source = "computed"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%s\t%s\n", s.SessionID, s.Role, s.ToolCalls, s.ReadCalls,
			s.SearchCalls, s.CommandCalls, s.EditCalls, ladoMillis(s.InvestigateMs), source); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeLadoTimingsSummary(out io.Writer, res ladoTimingsSummary) error {
	if _, err := fmt.Fprintf(out, "Project %s: %d tasks\n\n", res.ProjectID, len(res.Tasks)); err != nil {
		return err
	}
	if len(res.Tasks) == 0 {
		_, err := fmt.Fprintln(out, "No tasks with an issue yet.")
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if len(res.Gaps) > 0 {
		if _, err := fmt.Fprintln(tw, "FROM\tTO\tTASKS\tMEDIAN\tLONGEST"); err != nil {
			return err
		}
		for _, g := range res.Gaps {
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", g.From, g.To, g.Count, ladoMillis(g.MedianMs), ladoMillis(g.MaxMs)); err != nil {
				return err
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out); err != nil {
			return err
		}
	}
	if len(res.Investigation) > 0 {
		if _, err := fmt.Fprintln(tw, "ROLE\tSESSIONS\tMEDIAN TOOL CALLS\tMEDIAN READ+SEARCH\tMEDIAN INVESTIGATE"); err != nil {
			return err
		}
		for _, r := range res.Investigation {
			if _, err := fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\n", r.Role, r.Sessions, r.MedianCalls, r.MedianInvestigationCalls, ladoMillis(r.MedianInvestigateMs)); err != nil {
				return err
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(tw, "TASK\tISSUE\tTOTAL\tSTAGES\tRUNS\tTITLE"); err != nil {
		return err
	}
	for _, task := range res.Tasks {
		total := "-"
		if task.Timeline.TotalMs > 0 {
			total = ladoMillis(task.Timeline.TotalMs)
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\n", task.TaskID, ladoOrDash(task.IssueRef), total,
			len(task.Timeline.Points), task.Timeline.Runs, task.Title); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// ladoMillis prints a duration given in milliseconds to the second.
func ladoMillis(ms int64) string {
	return (time.Duration(ms) * time.Millisecond).Round(time.Second).String()
}

func ladoOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
