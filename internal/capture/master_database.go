package capture

import _ "embed"

//go:embed master_data.db
var embeddedMasterDatabase []byte
