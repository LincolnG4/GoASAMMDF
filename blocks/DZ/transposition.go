package DZ

type Transposition struct {
	//Decompressed ID
	DecompressedID string

	//Zip algorithm (Deflate or Transposition + Deflate)
	CompressType uint8

	//Decompressed size
	DecompressedLength uint64

	//Compressed size
	CompressedLength uint64

	//Parameter for transposition type (CompressType==1)
	Parameter uint32

	//DataBlock
	Datablock *[]byte
}

func (t *Transposition) Decompress() ([]byte, error) {
	data, err := Deflate(*t.Datablock)
	if err != nil {
		return nil, err
	}

	// calculate Matrix row and column
	cols := uint64(t.Parameter)
	rows := t.DecompressedLength / cols
	t.Transpose(rows, cols, &data)

	return data, nil
}

// Transpose applies traposition in the data block
func (t *Transposition) Transpose(rows uint64, cols uint64, data *[]byte) {
	arr := *data

	totalElements := uint64(len(arr))
	matrixElements := rows * cols
	remainingBytes := totalElements - matrixElements

	// Separate the remaining bytes
	var extraBytes []byte
	if remainingBytes != 0 {
		extraBytes = arr[matrixElements:]
		arr = arr[:matrixElements]
	}

	// Create a new array for the transposed data
	transposed := make([]byte, len(arr))

	var i, j uint64
	for j = 0; j < rows; j++ {
		for i = 0; i < cols; i++ {
			// Transpose the element from (i, j) in the original to (j, i)
			transposed[j*cols+i] = arr[i*rows+j]
		}
	}

	if remainingBytes != 0 {
		transposed = append(transposed, extraBytes...)
	}
	copy(*data, transposed)
}
