//go:build !wwdev

package main

// Public builds have no diagnostic outputs. Each function below is implemented in a wwdev file
// and is a no-op here so that shared code can call it unconditionally.

func (event combatEvent) formatNormal() string {
	var r0 string
	return r0
}

func marshalRuntimeIdentity(entity runtimeEntity) string {
	var r0 string
	return r0
}

func optionalDamageSource(value uint64, present bool) string {
	var r0 string
	return r0
}

func (p *processor) observeEncounterSummary(event combatEvent) {
}
