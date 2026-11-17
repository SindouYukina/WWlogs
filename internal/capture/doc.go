// Package capture is the shared combat core of WWlogs: capture and transport recovery, frame decryption and the
// current game protocol's decoders, RPC pairing, entity and role identity, damage attribution, encounters, effect
// lifecycles, statistics and the view snapshots and History records the product shows. Both build profiles run this
// one implementation. The Development diagnostics observe it from wwlogs/internal/devcapture through its observation
// points (Observer, the command census observers, SessionDiagnostics), which a Public build leaves unattached; the
// core never reads anything they compute. Main (cli.go) is the Public capture command line.
package capture
