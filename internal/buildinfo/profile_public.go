//go:build !wwdev

package buildinfo

// profile is the default when no build tag is given, so a build that forgets the development tag can
// only ever produce a public executable. Public builds compile out every raw diagnostic output.
const profile = ProfilePublic
