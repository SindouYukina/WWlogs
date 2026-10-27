package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestExtractCheckClientPushKeepsOnlyThreeVersionStrings(t *testing.T) {
	body := appendBytesField(nil, 1, []byte("3.7.0"))
	body = appendBytesField(body, 2, []byte("3.7.15"))
	body = appendBytesField(body, 3, []byte("3.7.16"))
	body = appendBytesField(body, 4, []byte("DEVICE-FINGERPRINT"))

	versions, err := extractCheckClientVersions(body)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(versions, ","), "3.7.0,3.7.15,3.7.16"; got != want {
		t.Fatalf("version fields = %q, want %q", got, want)
	}
	if strings.Contains(strings.Join(versions, " "), "DEVICE") {
		t.Fatal("non-version string leaked into extracted versions")
	}
}

func TestExtractLoginRequestBuildUsesOnlyFieldEightNineteen(t *testing.T) {
	const completeBuild = "release-3.7.0-win64-global-3.7.16"
	nested := appendBytesField(nil, 2, []byte("00000000-0000-0000-0000-000000000000"))
	nested = appendBytesField(nested, 15, []byte("1920x1080"))
	nested = appendBytesField(nested, 16, []byte("1920x1080"))
	nested = appendBytesField(nested, 19, []byte(completeBuild))
	nested = appendBytesField(nested, 20, []byte("GPU-MODEL-AND-DRIVER"))
	body := appendBytesField(nil, 8, nested)
	body = appendBytesField(body, 9, []byte("ACCOUNT-UID"))
	body = appendBytesField(body, 10, []byte("0123456789abcdef0123456789abcdef"))

	build, err := extractLoginRequestBuild(body)
	if err != nil {
		t.Fatal(err)
	}
	if build != completeBuild {
		t.Fatalf("build string = %q", build)
	}
	if strings.Contains(build, "DEVICE") || strings.Contains(build, "ACCOUNT") || strings.Contains(build, "GPU") || strings.Contains(build, "1920") {
		t.Fatalf("non-version LoginRequest data leaked: %q", build)
	}
}

func TestClientVersionObservationWaitsForHandshakeAndKeepsFirstValues(t *testing.T) {
	versions := appendBytesField(nil, 1, []byte("3.7.0"))
	versions = appendBytesField(versions, 2, []byte("3.7.15"))
	versions = appendBytesField(versions, 3, []byte("3.7.16"))
	const completeBuild = "release-3.7.0-win64-global-3.7.16"
	loginFields := appendBytesField(nil, 2, []byte("00000000-0000-0000-0000-000000000000"))
	loginFields = appendBytesField(loginFields, 15, []byte("1920x1080"))
	loginFields = appendBytesField(loginFields, 16, []byte("1920x1080"))
	loginFields = appendBytesField(loginFields, 19, []byte(completeBuild))
	loginFields = appendBytesField(loginFields, 20, []byte("GPU-MODEL-AND-DRIVER"))
	loginPayload := appendBytesField(nil, 8, loginFields)
	loginPayload = appendBytesField(loginPayload, 9, []byte("ACCOUNT-UID"))
	loginPayload = appendBytesField(loginPayload, 10, []byte("0123456789abcdef0123456789abcdef"))

	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.currentFrameTime = time.Date(2026, time.October, 27, 1, 2, 3, 0, time.UTC)
	if err := p.observeClientVersion(116, versions); err != nil {
		t.Fatal(err)
	}
	if p.clientVersion != nil {
		t.Fatalf("version was accepted before handshake: %+v", p.clientVersion)
	}
	p.sessionNumber = 1
	if err := p.observeClientVersion(116, versions); err != nil {
		t.Fatal(err)
	}
	if err := p.observeClientVersion(103, loginPayload); err != nil {
		t.Fatal(err)
	}
	if err := p.observeClientVersion(103, appendBytesField(nil, 8, appendBytesField(nil, 19, []byte("3.7.17.1")))); err != nil {
		t.Fatal(err)
	}
	got := p.clientVersion
	if got.SessionNumber != 1 || got.ClientBuild != completeBuild || strings.Join(got.CheckClientVersions, ",") != "3.7.0,3.7.15,3.7.16" {
		t.Fatalf("client version report = %+v", got)
	}
	if len(got.SourceCommands) != 2 || got.SourceCommands[0] != 103 || got.SourceCommands[1] != 116 {
		t.Fatalf("source command IDs = %v", got.SourceCommands)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitiveSibling := range [][]byte{
		[]byte("00000000-0000-0000-0000-000000000000"),
		[]byte("1920x1080"),
		[]byte("GPU-MODEL-AND-DRIVER"),
		[]byte("ACCOUNT-UID"),
		[]byte("0123456789abcdef0123456789abcdef"),
	} {
		if bytes.Contains(encoded, sensitiveSibling) {
			t.Fatalf("version report contains non-version request data: %s", encoded)
		}
	}
}

func TestClientVersionAuditFieldsContainOnlyVersionData(t *testing.T) {
	report := &clientVersionReport{
		ClientBuild:         "release-3.7.0-win64-global-3.7.16",
		CheckClientVersions: []string{"3.7.0", "3.7.15", "3.7.16"},
		SourceCommands:      []uint16{103, 116},
	}
	build, versions, commands := report.auditFields()
	if build != "release-3.7.0-win64-global-3.7.16" || versions != `["3.7.0","3.7.15","3.7.16"]` || commands != "103,116" {
		t.Fatalf("audit version fields = %q / %q / %q", build, versions, commands)
	}
}
