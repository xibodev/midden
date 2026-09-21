package adapter

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
)

// v1 fingerprinted payloads only. v2 also pins row identity and chronology.
const rowPrefixKind = "record-prefix-v2"

type rowFingerprint struct {
	digest hash.Hash
}

func newRowFingerprint() rowFingerprint {
	digest := sha256.New()
	digest.Write([]byte("midden.opencode-row-prefix/v2\x00"))
	return rowFingerprint{digest: digest}
}

func (f rowFingerprint) add(id string, createdMS int64, payload []byte) {
	var frame [8]byte
	binary.BigEndian.PutUint64(frame[:], uint64(len(id)))
	f.digest.Write(frame[:])
	f.digest.Write([]byte(id))
	binary.BigEndian.PutUint64(frame[:], uint64(createdMS))
	f.digest.Write(frame[:])
	binary.BigEndian.PutUint64(frame[:], uint64(len(payload)))
	f.digest.Write(frame[:])
	f.digest.Write(payload)
}

func (f rowFingerprint) sum() string {
	return "sha256:" + hex.EncodeToString(f.digest.Sum(nil))
}
