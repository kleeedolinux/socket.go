package socket

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"sync/atomic"
)

var idCounter uint64


func generateID() string {
	
	id := make([]byte, 8)
	_, err := io.ReadFull(rand.Reader, id)
	if err != nil {
		
		counter := atomic.AddUint64(&idCounter, 1)
		return hex.EncodeToString([]byte{
			byte(counter >> 56),
			byte(counter >> 48),
			byte(counter >> 40),
			byte(counter >> 32),
			byte(counter >> 24),
			byte(counter >> 16),
			byte(counter >> 8),
			byte(counter),
		})
	}

	
	counter := atomic.AddUint64(&idCounter, 1)
	return hex.EncodeToString(id) + "-" + hex.EncodeToString([]byte{
		byte(counter >> 56),
		byte(counter >> 48),
		byte(counter >> 40),
		byte(counter >> 32),
		byte(counter >> 24),
		byte(counter >> 16),
		byte(counter >> 8),
		byte(counter),
	})[:4]
}
