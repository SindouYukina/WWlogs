//go:build !wwdev

package capture

// equivalenceSinksSupported is false in the public profile: it has no diagnostic sink to attach.
const equivalenceSinksSupported = false

func attachEquivalenceSinks(*processor) *equivalenceSinkSet { return nil }
