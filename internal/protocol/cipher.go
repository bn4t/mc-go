package protocol

import (
	"crypto/aes"
	"crypto/cipher"
	"io"
)

// cfb8 is Java's "AES/CFB8/NoPadding": cipher-feedback with an 8-bit
// feedback size, so each processed byte shifts the 16-byte register
// left by one and appends the ciphertext byte. Go's crypto/cipher CFB
// is full-width feedback only, so this type implements cipher.Stream
// directly.
type cfb8 struct {
	block   cipher.Block
	reg     [16]byte
	decrypt bool
	tmp     [16]byte
}

func (c *cfb8) XORKeyStream(dst, src []byte) {
	if len(dst) < len(src) {
		panic("cfb8: dst smaller than src")
	}
	for i, b := range src {
		c.block.Encrypt(c.tmp[:], c.reg[:])
		out := b ^ c.tmp[0]
		dst[i] = out
		copy(c.reg[:], c.reg[1:])
		if c.decrypt {
			c.reg[15] = b
		} else {
			c.reg[15] = out
		}
	}
}

// cipherReader wraps r with the AES/CFB8 decrypt stream.
func cipherReader(r io.Reader, secret []byte) (io.Reader, error) {
	block, err := aes.NewCipher(secret)
	if err != nil {
		return nil, err
	}
	st := &cfb8{block: block, decrypt: true}
	copy(st.reg[:], secret)
	return &cipher.StreamReader{S: st, R: r}, nil
}

// cipherWriter wraps w with the AES/CFB8 encrypt stream.
func cipherWriter(w io.Writer, secret []byte) (io.Writer, error) {
	block, err := aes.NewCipher(secret)
	if err != nil {
		return nil, err
	}
	st := &cfb8{block: block}
	copy(st.reg[:], secret)
	return &cipher.StreamWriter{S: st, W: w}, nil
}

// CipherStreams arms AES/CFB8 on both directions of a connection —
// key and IV are both the shared secret, per vanilla's
// Crypt.cipherForSecretKey — and returns the wrapped reader/writer.
// Called after the login key exchange, matching
// Connection.setEncryptionKey.
func CipherStreams(r io.Reader, w io.Writer, secret []byte) (io.Reader, io.Writer, error) {
	cr, err := cipherReader(r, secret)
	if err != nil {
		return nil, nil, err
	}
	cw, err := cipherWriter(w, secret)
	if err != nil {
		return nil, nil, err
	}
	return cr, cw, nil
}
