package capture

import (
	"regexp"
)

// abnormalColumn matches the AbnormalDamageConfig columns Abnormal<id> that name negative-status DamageIds.
var abnormalColumn = regexp.MustCompile(`^abnormal(\d+)$`)
