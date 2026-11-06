package desktop

import "errors"

// licenseLinks is the only set of addresses the Licenses page can open. The frontend names them in
// frontend/src/lib/font-licenses.ts; a test keeps the two lists equal.
var licenseLinks = map[string]struct{}{
	"https://github.com/rsms/inter":         {},
	"https://github.com/notofonts/noto-cjk": {},
	"https://github.com/google/fonts":       {},
	"https://openfontlicense.org/":          {},
}

// OpenLicenseLink hands one official font or license address to the user's browser. License text itself is embedded, so
// reading it never needs this.
func (b *Bridge) OpenLicenseLink(address string) error {
	if _, known := licenseLinks[address]; !known {
		return errors.New("the address is not an official license link")
	}
	return b.openBrowser(address)
}
