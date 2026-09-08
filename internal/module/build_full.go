//go:build !headless

package module

// BuildVariant names the artifact this binary IS, not the source tree it came
// from.
//
// A build can legitimately exclude a face -- a module running behind a host
// does not need to serve its own UI. But a consumer pins a surface, and two
// artifacts that report identical identity while offering different faces
// cannot be pinned apart: the difference is discovered at runtime, which is the
// same defect class as a declared schema that does not match behaviour.
const BuildVariant = "standalone"
