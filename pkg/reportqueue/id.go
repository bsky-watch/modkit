package reportqueue

import (
	"crypto/cipher"
	"crypto/des"
	"encoding/hex"
	"math/big"
)

// IdCipher implements a simple encryption scheme to reversibly obscure
// internal report IDs before returning them to the user.
type IdCipher struct {
	base cipher.Block
}

func NewIdCipher(key string) (*IdCipher, error) {
	b, err := hex.DecodeString(key)
	if err != nil {
		return nil, err
	}
	c, err := des.NewCipher(b)
	if err != nil {
		return nil, err
	}
	return &IdCipher{base: c}, nil
}

func (c *IdCipher) Encrypt(n uint64) int64 {
	b := big.NewInt(0).SetUint64(n).FillBytes(make([]byte, c.base.BlockSize()))
	c.base.Encrypt(b, b)
	r := big.NewInt(0)
	r.SetBytes(b)
	// Not using r.Int64 because we're relying on Go's conversion behaviour
	// to preserve all bits during uint64 -> int64 conversion.
	return int64(r.Uint64())
}

func (c *IdCipher) Decrypt(n int64) uint64 {
	b := big.NewInt(0).SetUint64(uint64(n)).FillBytes(make([]byte, c.base.BlockSize()))
	c.base.Decrypt(b, b)
	r := big.NewInt(0)
	r.SetBytes(b)
	return r.Uint64()
}
