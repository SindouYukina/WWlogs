package capture

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var clientVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+._][0-9A-Za-z][0-9A-Za-z._+-]{0,63})?$`)

type clientVersionReport struct {
	SessionNumber       int       `json:"sessionNumber"`
	FirstObservedAt     time.Time `json:"firstObservedAt"`
	ClientBuild         string    `json:"clientBuild,omitempty"`
	CheckClientVersions []string  `json:"checkClientVersions,omitempty"`
	SourceCommands      []uint16  `json:"sourceCommands"`
}

func extractCheckClientVersions(data []byte) ([]string, error) {
	fields, err := parseProtoFields(data)
	if err != nil {
		return nil, fmt.Errorf("parse CheckClientPush: %w", err)
	}
	versions := make([]string, 0, 3)
	for _, field := range fields {
		if field.wire != 2 {
			continue
		}
		value := string(field.data)
		if clientVersionPattern.MatchString(value) {
			versions = append(versions, value)
		}
	}
	if len(versions) != 3 {
		return nil, fmt.Errorf("CheckClientPush has %d version-shaped strings; want 3", len(versions))
	}
	return versions, nil
}

func extractLoginRequestBuild(data []byte) (string, error) {
	fields, err := parseProtoFields(data)
	if err != nil {
		return "", fmt.Errorf("parse LoginRequest: %w", err)
	}
	field8Seen := false
	field19Shape := "absent"
	for _, field := range fields {
		if field.number != 8 || field.wire != 2 {
			continue
		}
		field8Seen = true
		nested, err := parseProtoFields(field.data)
		if err != nil {
			return "", fmt.Errorf("parse LoginRequest field 8: %w", err)
		}
		for _, value := range nested {
			if value.number != 19 || value.wire != 2 {
				if value.number == 19 {
					field19Shape = fmt.Sprintf("wire=%d", value.wire)
				}
				continue
			}
			build := string(value.data)
			if !utf8.Valid(value.data) || strings.TrimSpace(build) == "" {
				return "", fmt.Errorf("LoginRequest field 8.19 is not a non-empty UTF-8 version string")
			}
			for _, r := range build {
				if unicode.IsControl(r) {
					return "", fmt.Errorf("LoginRequest field 8.19 contains a control character")
				}
			}
			return build, nil
		}
	}
	return "", fmt.Errorf("LoginRequest field 8.19 is absent or invalid (field8=%t field19=%s)", field8Seen, field19Shape)
}

func (p *processor) observeClientVersion(commandID uint16, data []byte) error {
	if p.sessionNumber == 0 {
		return nil
	}
	var build string
	var versions []string
	switch commandID {
	case 103:
		parsed, err := extractLoginRequestBuild(data)
		if err != nil {
			return err
		}
		build = parsed
	case 116:
		parsed, err := extractCheckClientVersions(data)
		if err != nil {
			return err
		}
		versions = parsed
	default:
		return fmt.Errorf("unsupported version command %d", commandID)
	}
	if p.clientVersion == nil {
		p.clientVersion = &clientVersionReport{SessionNumber: p.sessionNumber, FirstObservedAt: p.currentFrameTime}
	}
	report := p.clientVersion
	if build != "" && report.ClientBuild == "" {
		report.ClientBuild = build
	}
	if len(versions) != 0 && len(report.CheckClientVersions) == 0 {
		report.CheckClientVersions = versions
	}
	for _, existing := range report.SourceCommands {
		if existing == commandID {
			return nil
		}
	}
	report.SourceCommands = append(report.SourceCommands, commandID)
	sort.Slice(report.SourceCommands, func(i, j int) bool { return report.SourceCommands[i] < report.SourceCommands[j] })
	return nil
}

func (report *clientVersionReport) auditFields() (build, versions, commands string) {
	if report == nil {
		return "", "", ""
	}
	encoded, _ := json.Marshal(report.CheckClientVersions)
	commandIDs := make([]string, 0, len(report.SourceCommands))
	for _, commandID := range report.SourceCommands {
		commandIDs = append(commandIDs, fmt.Sprint(commandID))
	}
	return report.ClientBuild, string(encoded), strings.Join(commandIDs, ",")
}
