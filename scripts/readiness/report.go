package readiness

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// WriteJSON writes the report as indented JSON.
func (r *Report) WriteJSON(w io.Writer) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(r)
}

// WriteMarkdown writes the Core Acceptance Matrix view of the report: one row per domain, then every check.
func (r *Report) WriteMarkdown(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Version readiness: game version %s\n\n", r.GameVersion)
	fmt.Fprintf(&b, "Generated %s. Protocol client %s; Master %s; image pack %s, resource %s.\n\n", r.Generated.Format("2006-01-02 15:04:05Z"), r.ProtocolClient, short(r.MasterSHA256), r.AssetClient, r.AssetResource)
	fmt.Fprintf(&b, "- STATIC_READY: **%v**\n- LIVE_VERIFIED: **%v**\n\n", r.StaticReady, r.LiveVerified)
	if len(r.Blockers) > 0 {
		b.WriteString("Blockers:\n\n")
		for _, blocker := range r.Blockers {
			fmt.Fprintf(&b, "- %s\n", escape(blocker))
		}
		b.WriteString("\n")
	}
	b.WriteString("| Domain | Result | Checks |\n| --- | --- | --- |\n")
	for _, d := range r.Domains {
		fmt.Fprintf(&b, "| %s | %s | %d |\n", d.Title, d.Status, d.Checks)
	}
	b.WriteString("\n| Check | Domain | Kind | Mandatory | Result | Coverage | Evidence / reason |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, c := range r.Checks {
		coverage := ""
		if c.Coverage != nil {
			coverage = fmt.Sprintf("%d/%d %s", c.Coverage.Passed, c.Coverage.Total, c.Coverage.Denominator)
		}
		var notes []string
		if c.Reason != "" {
			notes = append(notes, c.Reason)
		}
		notes = append(notes, c.Findings...)
		if c.MoreFindings > 0 {
			notes = append(notes, fmt.Sprintf("... %d more findings", c.MoreFindings))
		}
		if len(notes) == 0 {
			notes = c.Evidence
		}
		fmt.Fprintf(&b, "| `%s` %s | %s | %s | %v | %s | %s | %s |\n", c.ID, escape(c.Title), c.Domain, c.Kind, c.Mandatory, c.Status, escape(coverage), escape(strings.Join(notes, "; ")))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func escape(text string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(text)
}
