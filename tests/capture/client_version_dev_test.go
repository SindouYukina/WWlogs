//go:build wwdev

package capture

import (
	"encoding/json"
	"testing"
)

func TestClientVersionAuditFieldsContainOnlyVersionData(t *testing.T) {
	report := &ClientVersionReport{
		ClientBuild:         currentTestBuild,
		CheckClientVersions: currentTestClientVersions,
		SourceCommands:      []uint16{CmdLoginRequest, CmdCheckClientPush},
	}
	build, versions, commands := clientVersionAuditFields(report)
	expectedVersions, err := json.Marshal(currentTestClientVersions)
	if err != nil {
		t.Fatal(err)
	}
	if build != currentTestBuild || versions != string(expectedVersions) || commands != "103,116" {
		t.Fatalf("audit version fields = %q / %q / %q", build, versions, commands)
	}
}
