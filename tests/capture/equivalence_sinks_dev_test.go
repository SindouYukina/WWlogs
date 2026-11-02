//go:build wwdev

package capture

// equivalenceSinksSupported reports that this profile has diagnostic sinks the collector can attach.
const equivalenceSinksSupported = true

// attachEquivalenceSinks connects hashing writers to the diagnostic sinks that carry event, lifecycle,
// and snapshot records. Attaching them must not change the core state, which the collector verifies.
func attachEquivalenceSinks(p *processor) *equivalenceSinkSet {
	set := &equivalenceSinkSet{sinks: map[string]*equivalenceHashSink{
		"combatEventsJSONL": newEquivalenceHashSink(),
		"buffLifecycle":     newEquivalenceHashSink(),
		"buffSnapshots":     newEquivalenceHashSink(),
	}}
	p.combatEventsJSONL = set.sinks["combatEventsJSONL"]
	p.buffLifecycle = set.sinks["buffLifecycle"]
	p.buffSnapshots = set.sinks["buffSnapshots"]
	return set
}
