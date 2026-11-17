package capture

import ()

type diagnosticSetupError struct{ err error }

func (e *diagnosticSetupError) Error() string { return e.err.Error() }
func (e *diagnosticSetupError) Unwrap() error { return e.err }
