package capture

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var clientVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+._][0-9A-Za-z][0-9A-Za-z._+-]{0,63})?$`)

type ClientVersionReport struct {
	SessionNumber       int       `json:"sessionNumber"`
	FirstObservedAt     time.Time `json:"firstObservedAt"`
	ClientBuild         string    `json:"clientBuild,omitempty"`
	CheckClientVersions []string  `json:"checkClientVersions,omitempty"`
	SourceCommands      []uint16  `json:"sourceCommands"`
}

func extractCheckClientVersions(data []byte) ([]string, error) {
	fields, err := ParseProtoFields(data)
	if err != nil {
		return nil, fmt.Errorf("parse CheckClientPush: %w", err)
	}
	versions := make([]string, 0, 3)
	for _, field := range fields {
		if field.Wire != 2 {
			continue
		}
		value := string(field.Data)
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
	fields, err := ParseProtoFields(data)
	if err != nil {
		return "", fmt.Errorf("parse LoginRequest: %w", err)
	}
	basicInfoSeen := false
	versionShape := "absent"
	for _, field := range fields {
		if field.Number != FieldLoginRequestClientBasicInfo || field.Wire != 2 {
			continue
		}
		basicInfoSeen = true
		nested, err := ParseProtoFields(field.Data)
		if err != nil {
			return "", fmt.Errorf("parse LoginRequest.ClientBasicInfo: %w", err)
		}
		for _, value := range nested {
			if value.Number != FieldClientBasicInfoClientVersion || value.Wire != 2 {
				if value.Number == FieldClientBasicInfoClientVersion {
					versionShape = fmt.Sprintf("wire=%d", value.Wire)
				}
				continue
			}
			build := string(value.Data)
			if !utf8.Valid(value.Data) || strings.TrimSpace(build) == "" {
				return "", fmt.Errorf("LoginRequest.ClientBasicInfo.ClientVersion is not a non-empty UTF-8 version string")
			}
			for _, r := range build {
				if unicode.IsControl(r) {
					return "", fmt.Errorf("LoginRequest.ClientBasicInfo.ClientVersion contains a control character")
				}
			}
			return build, nil
		}
	}
	return "", fmt.Errorf("LoginRequest.ClientBasicInfo.ClientVersion is absent or invalid (ClientBasicInfo=%t ClientVersion=%s)", basicInfoSeen, versionShape)
}

func (p *Processor) observeClientVersion(commandID uint16, data []byte) error {
	if p.SessionNumber == 0 {
		return nil
	}
	var build string
	var versions []string
	switch commandID {
	case CmdLoginRequest:
		parsed, err := extractLoginRequestBuild(data)
		if err != nil {
			return err
		}
		build = parsed
	case CmdCheckClientPush:
		parsed, err := extractCheckClientVersions(data)
		if err != nil {
			return err
		}
		versions = parsed
	default:
		return fmt.Errorf("unsupported version command %d", commandID)
	}
	// The session's own report decides whether it is decoded (protocol_session.go); the census report below keeps the first.
	before := p.Protocol.State
	if build != "" {
		p.Protocol.ObserveVersion(gameVersionOf(build))
	} else {
		p.Protocol.ObserveVersion(gameVersionOf(versions[0]))
	}
	if before == ProtocolPending && p.Protocol.State == ProtocolCurrent {
		// From here on the session is decoded; Combat History records which game version everything of it came from.
		p.DamageStatistics.confirmProtocolSession(p.SessionNumber, p.Protocol.gameVersion)
	}
	if p.ClientVersion == nil {
		p.ClientVersion = &ClientVersionReport{SessionNumber: p.SessionNumber, FirstObservedAt: p.CurrentFrameTime}
	}
	report := p.ClientVersion
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
