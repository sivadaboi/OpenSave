package syncengine

// availableDiskBytes reports free space on the volume holding dir (ok=false
// when undeterminable, so the caller skips the check). It's a package var so
// tests can simulate a full disk.
var availableDiskBytes = diskFreeBytes
