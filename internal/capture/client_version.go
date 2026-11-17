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
	basicInfoSeen := false
	versionShape := "absent"
	for _, field := range fields {
		if field.number != fieldLoginRequestClientBasicInfo || field.wire != 2 {
			continue
		}
		basicInfoSeen = true
		nested, err := parseProtoFields(field.data)
		if err != nil {
			return "", fmt.Errorf("parse LoginRequest.ClientBasicInfo: %w", err)
		}
		for _, value := range nested {
			if value.number != fieldClientBasicInfoClientVersion || value.wire != 2 {
				if value.number == fieldClientBasicInfoClientVersion {
					versionShape = fmt.Sprintf("wire=%d", value.wire)
				}
				continue
			}
			build := string(value.data)
			if !utf8.Valid(value.data) || strings.TrimSpace(build) == "" {
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

func (p *processor) observeClientVersion(commandID uint16, data []byte) error {
	if p.sessionNumber == 0 {
		return nil
	}
	var build string
	var versions []string
	switch commandID {
	case cmdLoginRequest:
		parsed, err := extractLoginRequestBuild(data)
		if err != nil {
			return err
		}
		build = parsed
	case cmdCheckClientPush:
		parsed, err := extractCheckClientVersions(data)
		if err != nil {
			return err
		}
		versions = parsed
	default:
		return fmt.Errorf("unsupported version command %d", commandID)
	}
	// The session's own report decides whether it is decoded (protocol_session.go); the census report below keeps the first.
	before := p.protocol.state
	if build != "" {
		p.protocol.observeVersion(gameVersionOf(build))
	} else {
		p.protocol.observeVersion(gameVersionOf(versions[0]))
	}
	if before == protocolPending && p.protocol.state == protocolCurrent {
		// From here on the session is decoded; Combat History records which game version everything of it came from.
		p.damageStatistics.confirmProtocolSession(p.sessionNumber, p.protocol.gameVersion)
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
