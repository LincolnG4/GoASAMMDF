package mf4

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"

	"github.com/LincolnG4/GoMDF/internal/blocks"
	"github.com/LincolnG4/GoMDF/internal/blocks/AT"
	"github.com/LincolnG4/GoMDF/internal/blocks/CG"
	"github.com/LincolnG4/GoMDF/internal/blocks/CN"
	"github.com/LincolnG4/GoMDF/internal/blocks/DG"
	"github.com/LincolnG4/GoMDF/internal/blocks/FH"
	"github.com/LincolnG4/GoMDF/internal/blocks/HD"
	"github.com/LincolnG4/GoMDF/internal/blocks/ID"
	"github.com/LincolnG4/GoMDF/internal/blocks/MD"
	"github.com/LincolnG4/GoMDF/internal/blocks/TX"
	"github.com/davecgh/go-spew/spew"
)

type MF4 struct {
	File           *os.File
	Identification *ID.Block
	FileHistory    []*FH.Block
	Attachments    []*AT.Block
	ChannelGroup   []*ChannelGroup
}

type ChannelGroup struct {
	Block     *CG.Block
	Channels  map[string]*CN.Block
	Datagroup *DG.Block
}

func ReadFile(file *os.File, getXML bool) (*MF4, error) {
	var address int64 = 0

	//fileInfo, _ := file.Stat()
	//fileSize := fileInfo.Size()

	mf4File := MF4{File: file}

	//Load Identification IDBlock
	idBlock := ID.New(file, address)
	mf4File.Identification = idBlock

	fmt.Printf("%s %s %s %s %d %s \n", idBlock.File,
		idBlock.Version,
		idBlock.Program,
		idBlock.Reserved1,
		idBlock.VersionNumber,
		idBlock.Reserved2)

	fileVersion := idBlock.VersionNumber

	if fileVersion < 400 {
		return nil, &VersionError{}
	}

	if fileVersion >= 400 {
		mf4File.read(getXML)
	}

	return &mf4File, nil
}

func (m *MF4) read(getXML bool) {
	var file *os.File = m.File

	if m.Identification.UnfinalizedFlag != 0 {
		panic("NOT FINALIZED, CODE NOTE PREPARE FOR IT")
	}

	//Create MF4 struct from the file
	//Get HDBLOCK
	hdBlock := HD.New(file, blocks.IdblockSize)

	//From HDBLOCK read File History
	m.loadFileHistory(file, hdBlock.Link.FhFirst, getXML)
	version := m.Identification.VersionNumber

	//From HDBLOCK read Attachments
	//Get all AT
	m.loadAttachmemt(file, hdBlock.Link.AtFirst)

	//From HDBLOCK read DataGroup
	NextAddressDG := hdBlock.Link.DgFirst
	index := 0

	//Get all DataGroup
	for NextAddressDG != 0 {
		//Create DGBlock and append to MF4
		dgBlock := DG.New(file, NextAddressDG)

		//Read MdBlocks inside
		mdCommentAddr := dgBlock.Link.MdComment
		if mdCommentAddr != 0 {
			mdBlock := MD.ReadMdComment(file, mdCommentAddr)
			comment := mdBlock.Data.Value
			fmt.Printf("%s\n", comment)
		}

		//From DGBLOCK read ChannelGroup
		NextAddressCG := dgBlock.Link.CgFirst
		indexCG := 0

		for NextAddressCG != 0 {
			cgBlock := CG.New(file, version, NextAddressCG)
			//Save Informations
			channelGroup:=&ChannelGroup{
				Block: cgBlock,
				Channels: make(map[string]*CN.Block),
				Datagroup: dgBlock,
			} 

			//From CGBLOCK read Channel
			nextAddressCN := cgBlock.Link.CnFirst
			indexCN := 0

			for nextAddressCN != 0 {
				cnBlock := CN.New(file, version, nextAddressCN)

				//Get Name
				txBlock := TX.New(file, int64(cnBlock.Link.TxName))
					
				//Remove 00 bytes from the name
				channelName := string(bytes.Trim(txBlock.Data.TxData, "\x00"))
				channelGroup.Channels[channelName] = cnBlock

				//Get XML comments
				MdCommentAdress := cnBlock.Link.MdComment
				if getXML && MdCommentAdress != 0 {
					mdBlock := MD.Block{}
					mdBlock.New(file, MdCommentAdress)
				} else {
					mdBlock := (&MD.Block{}).BlankBlock()
					mdComment := ""
					fmt.Print(mdComment, mdBlock, "\n")
				}

				
				nextAddressCN = cnBlock.Link.Next
				indexCN++

			}

			m.ChannelGroup = append(m.ChannelGroup, channelGroup)	
			NextAddressCG = cgBlock.Link.Next
			indexCG++
		}

		fmt.Println("\n##############################")

		NextAddressDG = dgBlock.Link.Next
		index++
	}

}

// ChannelNames returns the sample data from a signal
func (m *MF4) ChannelNames() []string {
	channelNames := make([]string, 0)

	for _,cg := range m.ChannelGroup {
		for key := range cg.Channels{
			channelNames = append(channelNames, key)
		}
	}

	return channelNames
}

//GetChannelSample load sample by Channel Name 
func (m *MF4) GetChannelSample(channelName string) ([]interface{},error) {
	var byteOrder binary.ByteOrder

	for _, cgrp := range m.ChannelGroup {
		cn, ok := cgrp.Channels[channelName]
		if !ok {
			continue
		}
		dg := cgrp.Datagroup
		cg := cgrp.Block
		file := m.File

		dataType := cn.Data.DataType

		readAddr := blocks.HeaderSize + dg.Link.Data + int64(dg.Data.RecIDSize) + int64(cn.Data.ByteOffset)
		size := (cn.Data.BitCount + uint32(cn.Data.BitOffset)) / 8
		data := make([]byte, size)
		sample := make([]interface{}, 0)

		rowSize := int64(cg.Data.DataBytes)

		if dataType == 0 || dataType == 2 || dataType == 4 || dataType == 8 || dataType == 15 {
			byteOrder = binary.LittleEndian
		} else {
			byteOrder = binary.BigEndian
		}

		dtype := loadDataType(dataType, len(data))

		// Create a new instance of the data type using reflection
		sliceElemType := reflect.TypeOf(dtype)
		sliceElem := reflect.New(sliceElemType).Interface()

		for i := uint64(0); i < cg.Data.CycleCount; i += 1 {
			seekRead(file, readAddr, data)

			buf := bytes.NewBuffer(data)
			err := binary.Read(buf, byteOrder, sliceElem)
			if err != nil {
				fmt.Println("Error reading:", err)
				return nil,  errors.New("parsing channel error")
			}
			sample = append(sample, reflect.ValueOf(sliceElem).Elem().Interface())
			readAddr += rowSize
		}

		return sample, nil
	}
	return nil, errors.New("channel doen't exist")
}
func seekRead(file *os.File, readAddr int64, data []byte) {
	_, errs := file.Seek(readAddr, 0)
	if errs != nil {
		if errs != io.EOF {
			fmt.Println(errs, "Memory Addr out of size")
		}
	}
	_, err := file.Read(data)
	if err != nil {
		if err != io.EOF {
			fmt.Println("LoadBuffer error: ", err)
		}
	}

}

func loadDataType(dataType uint8, lenSize int) interface{} {
	var dtype interface{}
	switch dataType {
	case 0, 1:
		switch lenSize {
		case 1:
			dtype = uint8(0)
		case 2:
			dtype = uint16(0)
		case 4:
			dtype = uint32(0)
		case 8:
			dtype = uint64(0)
		}
	case 2, 3:
		switch lenSize {
		case 1:
			dtype = int8(0)
		case 2:
			dtype = int16(0)
		case 4:
			dtype = int32(0)
		case 8:
			dtype = int64(0)
		}

	case 4, 5:
		switch lenSize {
		case 4:
			dtype = float32(0)
		case 8:
			dtype = float64(0)

		}

	}

	return dtype
}

// loadAttachmemt iterates over all AT blocks and append array to MF4 object
func (m *MF4) loadAttachmemt(file *os.File, startAddressAT int64) {
	var index int = 0
	array := make([]*AT.Block, 0)
	nextAddressAT := startAddressAT

	for nextAddressAT != 0 {
		atBlock := AT.New(file, nextAddressAT)

		txBlock := TX.New(file, atBlock.Link.TXFilename)
		filename := txBlock.Data.TxData
		fmt.Printf("Filename attached: %s\n", filename)

		txBlock = TX.New(file, atBlock.Link.TXMimetype)
		mime := txBlock.Data.TxData
		fmt.Printf("Mime attached: %s\n", mime)

		//Read MDComment
		MdCommentAdress := atBlock.Link.MDComment
		if MdCommentAdress != 0 {
			mdBlock := MD.ReadMdComment(file, MdCommentAdress)
			comment := mdBlock.Data.Value
			fmt.Printf("%s\n", comment)
		}

		array = append(array, atBlock)
		nextAddressAT = atBlock.Link.Next
		index++
	}
	m.Attachments = array

}

// LoadFileHistory iterates over all FH blocks and append array to MF4 object
func (m *MF4) loadFileHistory(file *os.File, startAddressFH int64, getXML bool) {
	var index int = 0
	array := make([]*FH.Block, 0)
	nextAddressFH := startAddressFH

	//iterate over all FH blocks
	for nextAddressFH != 0 {
		fhBlock := FH.New(file, nextAddressFH)
		MdCommentAdress := fhBlock.Link.MDComment

		//Read MDComment
		if MdCommentAdress != 0 {
			comment := MD.ReadMdComment(file, MdCommentAdress)
			fmt.Printf("%s\n", comment.Data)
		}

		array = append(array, fhBlock)

		nextAddressFH = fhBlock.Link.Next
		index++

	}
	m.FileHistory = array

}

// Version method returns the MDF file version
func (m *MF4) Version() string {
	return string(m.Identification.Version[:])
}

func debug(file *os.File, offset int64, size int) {
	_, err := file.Seek(int64(offset), io.SeekStart)
	if err != nil {
		panic(err)
	}
	buf := make([]byte, size)
	n, err := file.Read(buf[:cap(buf)])
	buf = buf[:n]
	if err != nil {
		if err != io.EOF {
			panic(err)
		}
	}
	spew.Dump(buf)
}
