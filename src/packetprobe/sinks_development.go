//go:build wwdev

package main

import "io"

// diagWriter is the type of every raw diagnostic sink. In a development build it is an ordinary writer.
type diagWriter = io.Writer
