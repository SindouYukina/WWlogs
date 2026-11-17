package capture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// currentTestClientVersions are the CheckClientPush versions the 2026-11-12 Global 3.8 capture reported (app, launcher,
// resource); currentTestBuild is its LoginRequest build.
var currentTestClientVersions = []string{"3.8.0", "3.8.8", "3.8.9"}

func observedCheckClientVersionsPayload() []byte {
	var payload []byte
	for index, field := range []uint64{fieldCheckClientPushAppVersion, fieldCheckClientPushLauncherVersion, fieldCheckClientPushResourceVersion} {
		payload = appendBytesField(payload, field, []byte(currentTestClientVersions[index]))
	}
	return payload
}

func TestExtractCheckClientPushKeepsOnlyThreeVersionStrings(t *testing.T) {
	body := observedCheckClientVersionsPayload()
	body = appendBytesField(body, 4, []byte("DEVICE-FINGERPRINT")) // a field CheckClientPush does not declare

	versions, err := extractCheckClientVersions(body)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(versions, ","), strings.Join(currentTestClientVersions, ","); got != want {
		t.Fatalf("version fields = %q, want %q", got, want)
	}
	if strings.Contains(strings.Join(versions, " "), "DEVICE") {
		t.Fatal("non-version string leaked into extracted versions")
	}
}

func TestExtractLoginRequestBuildUsesOnlyTheClientVersion(t *testing.T) {
	completeBuild := currentTestBuild
	nested := appendBytesField(nil, fieldClientBasicInfoDeviceID, []byte("00000000-0000-0000-0000-000000000000"))
	nested = appendBytesField(nested, fieldClientBasicInfoScreenHeight, []byte("1920x1080"))
	nested = appendBytesField(nested, fieldClientBasicInfoScreenWidth, []byte("1920x1080"))
	nested = appendBytesField(nested, FieldClientBasicInfoClientVersion, []byte(completeBuild))
	nested = appendBytesField(nested, fieldClientBasicInfoOSVersion, []byte("GPU-MODEL-AND-DRIVER"))
	body := appendBytesField(nil, FieldLoginRequestClientBasicInfo, nested)
	body = appendBytesField(body, fieldLoginRequestDevLoginCheckData, []byte("ACCOUNT-UID"))
	body = appendBytesField(body, fieldLoginRequestPublicResourceVersionInfo, []byte("0123456789abcdef0123456789abcdef"))

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
	versions := observedCheckClientVersionsPayload()
	completeBuild := currentTestBuild
	loginFields := appendBytesField(nil, fieldClientBasicInfoDeviceID, []byte("00000000-0000-0000-0000-000000000000"))
	loginFields = appendBytesField(loginFields, fieldClientBasicInfoScreenHeight, []byte("1920x1080"))
	loginFields = appendBytesField(loginFields, fieldClientBasicInfoScreenWidth, []byte("1920x1080"))
	loginFields = appendBytesField(loginFields, FieldClientBasicInfoClientVersion, []byte(completeBuild))
	loginFields = appendBytesField(loginFields, fieldClientBasicInfoOSVersion, []byte("GPU-MODEL-AND-DRIVER"))
	loginPayload := appendBytesField(nil, FieldLoginRequestClientBasicInfo, loginFields)
	loginPayload = appendBytesField(loginPayload, fieldLoginRequestDevLoginCheckData, []byte("ACCOUNT-UID"))
	loginPayload = appendBytesField(loginPayload, fieldLoginRequestPublicResourceVersionInfo, []byte("0123456789abcdef0123456789abcdef"))

	p := NewProcessor(nil, &bytes.Buffer{})
	p.CurrentFrameTime = time.Date(2026, time.October, 27, 1, 2, 3, 0, time.UTC)
	if err := p.observeClientVersion(CmdCheckClientPush, versions); err != nil {
		t.Fatal(err)
	}
	if p.ClientVersion != nil {
		t.Fatalf("version was accepted before handshake: %+v", p.ClientVersion)
	}
	p.SessionNumber = 1
	if err := p.observeClientVersion(CmdCheckClientPush, versions); err != nil {
		t.Fatal(err)
	}
	if err := p.observeClientVersion(CmdLoginRequest, loginPayload); err != nil {
		t.Fatal(err)
	}
	if err := p.observeClientVersion(CmdLoginRequest, appendBytesField(nil, FieldLoginRequestClientBasicInfo, appendBytesField(nil, FieldClientBasicInfoClientVersion, []byte(currentTestClientVersions[2]+".1")))); err != nil {
		t.Fatal(err)
	}
	got := p.ClientVersion
	if got.SessionNumber != 1 || got.ClientBuild != completeBuild || strings.Join(got.CheckClientVersions, ",") != strings.Join(currentTestClientVersions, ",") {
		t.Fatalf("client version report = %+v", got)
	}
	if len(got.SourceCommands) != 2 || got.SourceCommands[0] != CmdLoginRequest || got.SourceCommands[1] != CmdCheckClientPush {
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
