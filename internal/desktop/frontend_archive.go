package desktop

// frontendArchive is a ZIP of the built frontend. The tracked value is empty; scripts/embed-key.go replaces this file
// through a temporary Go overlay with the archive of a frontend built from the tracked sources and lockfile, so no
// build output ever has to exist in the repository. A string keeps it in read-only executable memory. Without it the host
// serves a page that says the frontend is missing.
var frontendArchive string
