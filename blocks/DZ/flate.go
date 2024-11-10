package DZ

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
)

type Flate struct {
	//Decompressed ID
	DecompressedID string

	//Zip algorithm (Deflate==0 or Transposition + Deflate==1)
	CompressType uint8

	//Decompressed size
	DecompressedLength uint64

	//Compressed size
	CompressedLength uint64

	//DataBlock
	Datablock *[]byte
}

func (f *Flate) Decompress() ([]byte, error) {
	return Deflate(*f.Datablock)
}

func Deflate(data []byte) ([]byte, error) {
	// Create a zlib reader directly from the file's limited reader
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create reader: %v", err)
	}
	defer reader.Close()

	// Read and return the decompressed data
	return io.ReadAll(reader)
}
