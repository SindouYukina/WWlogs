package capture

import ()

type DiagnosticSetupError struct{ Err error }

func (e *DiagnosticSetupError) Error() string { return e.Err.Error() }
func (e *DiagnosticSetupError) Unwrap() error { return e.Err }
