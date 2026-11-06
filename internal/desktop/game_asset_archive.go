package desktop

// gameAssetArchive is the pack of product game images. The tracked value is empty; scripts/embed-key.go replaces this
// file through a temporary Go overlay in Public builds, from assets/manifest.json. A string keeps the bytes in read-only
// executable memory, where untouched images cost no RAM.
var gameAssetArchive string
