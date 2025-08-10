package countminsketch

import "encoding/binary"

const (
	prime1 = 0x9E3779B1
	prime2 = 0x85EBCA77
	prime3 = 0xC2B2AE3D
	prime4 = 0x27D4EB2F
	prime5 = 0x165667B1
)

// hash computes a hash value for the given item using a simple hash function.
func hash(item string, seed uint32) uint32 {
	data := []byte(item)
	//nolint:gosec // Overflow is not a concern here, we are using uint32
	length := uint32(len(data))

	var h32 uint32

	if length >= 16 {
		// Process 16-byte chunks
		v1 := seed + prime1 + prime2
		v2 := seed + prime2
		v3 := seed
		v4 := seed - prime1

		for len(data) >= 16 {
			v1 = rotl32(v1+binary.LittleEndian.Uint32(data[0:4])*prime2, 13) * prime1
			v2 = rotl32(v2+binary.LittleEndian.Uint32(data[4:8])*prime2, 13) * prime1
			v3 = rotl32(v3+binary.LittleEndian.Uint32(data[8:12])*prime2, 13) * prime1
			v4 = rotl32(v4+binary.LittleEndian.Uint32(data[12:16])*prime2, 13) * prime1
			data = data[16:]
		}

		h32 = rotl32(v1, 1) + rotl32(v2, 7) + rotl32(v3, 12) + rotl32(v4, 18)
	} else {
		h32 = seed + prime5
	}

	h32 += length

	// Process remaining bytes
	for len(data) >= 4 {
		h32 += binary.LittleEndian.Uint32(data[0:4]) * prime3
		h32 = rotl32(h32, 17) * prime4
		data = data[4:]
	}

	for len(data) > 0 {
		h32 += uint32(data[0]) * prime5
		h32 = rotl32(h32, 11) * prime1
		data = data[1:]
	}

	// Avalanche
	h32 ^= h32 >> 15
	h32 *= prime2
	h32 ^= h32 >> 13
	h32 *= prime3
	h32 ^= h32 >> 16

	return h32
}

// It shifts the bits to the left by r positions and wraps around the bits that fall off.
func rotl32(x uint32, r uint8) uint32 {
	return (x << r) | (x >> (32 - r))
}
