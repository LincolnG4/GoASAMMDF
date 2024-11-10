package mf4

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/LincolnG4/GoMDF/blocks"
	"github.com/LincolnG4/GoMDF/blocks/AT"
	"github.com/LincolnG4/GoMDF/blocks/CG"
	"github.com/LincolnG4/GoMDF/blocks/CN"
	"github.com/LincolnG4/GoMDF/blocks/DL"
	"github.com/LincolnG4/GoMDF/blocks/EV"
	"github.com/LincolnG4/GoMDF/blocks/FH"
	"github.com/LincolnG4/GoMDF/blocks/HD"
	"github.com/LincolnG4/GoMDF/blocks/ID"
	"github.com/LincolnG4/GoMDF/blocks/MD"
	"github.com/LincolnG4/GoMDF/blocks/SI"
	"github.com/LincolnG4/GoMDF/blocks/TX"
	"github.com/davecgh/go-spew/spew"
)

type MF4 struct {
	File           *os.File
	Header         *HD.Block
	Identification *ID.Block

	//Address to First File History Block
	FileHistory int64

	DataGroups   []DataGroup
	ChannelGroup []ChannelGroup
	Channels     []Channel

	//Unsorted
	UnsortedBlocks []UnsortedBlock

	ReadOptions *ReadOptions
}

type ReadOptions struct {
	// MemoryOptimized indicates whether to store data in memory or use
	// file-based storage.
	// Default is false, measures are cached in memory, which can improve
	// performance by avoiding file I/O operations but may increase memory usage
	// , particularly with large datasets.
	//
	// If true, measures are saved to a file or re-read as needed. This approach
	// helps manage memory usage more effectively by offloading data to disk,
	// making it suitable for very large datasets that might exceed available
	// memory
	MemoryOptimized bool

	// InitAllChannels indicates whether to read all channels during
	// initialization.
	// If true, all predefined channels will be created and ready for use
	// immediately after initialization. This can be useful when you need all
	// channels available from the start and want to avoid delays caused by
	// creating channels later on.
	//
	// If false, channels are created on-demand as they are needed. This
	// approach can be more memory-efficient if you have a large number of
	// channels or don't need all of them immediately. It also avoids
	// preallocating resources that might never be used
	//InitAllChannels bool
}

type UnsortedBlock struct {
	dataGroup         *DataGroup
	channelGroupsByID map[uint64]*ChannelGroup
}

func ReadFile(file *os.File, readOptions *ReadOptions) (*MF4, error) {
	var address int64 = 0
	mf4File := MF4{
		File:           file,
		Identification: ID.New(file, address),
		ReadOptions:    readOptions,
	}
	fileVersion := mf4File.MdfVersion()
	if fileVersion < 400 {
		return nil, fmt.Errorf("file version is not >= 4.00")
	}

	if fileVersion >= 400 {
		mf4File.loadHeader()
		mf4File.loadFirstFileHistory()
		mf4File.read()
	}
	return &mf4File, nil
}

func (m *MF4) read() {
	var file *os.File = m.File
	var comment string

	if !m.IsFinalized() {
		panic("MF4 NOT FINALIZED, PACKAGE IS NOT PREPARED")
	}

	version := m.MdfVersion()
	nextDataGroupAddress := m.firstDataGroup()
	m.Channels = make([]Channel, 0)

	dgindex := 0
	for nextDataGroupAddress != 0 {
		var dataGroup DataGroup
		var UnsortedBlocks UnsortedBlock
		isUnsorted := false

		dataGroup = NewDataGroup(file, nextDataGroupAddress)

		comment = MD.New(file, dataGroup.block.MetadataComment())

		nextAddressCG := dataGroup.block.FirstChannelGroup()
		cgIndex := 0
		for nextAddressCG != 0 {
			var masterChannel Channel

			cgBlock, err := CG.New(file, version, nextAddressCG)
			if err != nil {
				panic(err)
			}

			channelGroup := &ChannelGroup{
				Block:      cgBlock,
				Channels:   make(map[string]*Channel),
				DataGroup:  dataGroup.block,
				SourceInfo: SI.Get(file, version, cgBlock.Link.SiAcqSource),
				Comment:    comment,
			}

			nextAddressCN := cgBlock.FirstChannel()
			for nextAddressCN != 0 {
				cnBlock, err := CN.New(file, version, nextAddressCN)
				if err != nil {
					panic(err)
				}

				cc, err := cnBlock.Conversion(m.File, cnBlock.DataType())
				if err != nil {
					panic(err)
				}

				cn := &Channel{
					Name:              cnBlock.ChannelName(m.File),
					ChannelGroup:      cgBlock,
					ChannelGroupIndex: cgIndex,
					DataGroup:         &dataGroup,
					DataGroupIndex:    dgindex,
					Type:              cnBlock.Type(),
					Master:            &masterChannel,
					SourceInfo:        SI.Get(file, version, cnBlock.Link.SiSource),
					Comment:           MD.New(file, cnBlock.CommentMd()),
					Conversion:        cc,
					block:             cnBlock,
					isUnsorted:        false,
					mf4:               m,
				}

				// save master channel address
				if cnBlock.IsMaster() {
					masterChannel = *cn
					cn.Master = nil
				}

				// Unsorted file
				if dataGroup.block.Data.RecIDSize != 0 {
					cn.CachedSamples = make([]interface{}, 0)
					isUnsorted = true
					if UnsortedBlocks.dataGroup == nil {
						UnsortedBlocks = newUnsortedGroup(dataGroup)
					}

					UnsortedBlocks.channelGroupsByID[cgBlock.Data.RecordId] = channelGroup
					if cnBlock.Link.Data != 0 {
						vsldMap := make(map[string]*Channel)
						vsldMap["vlsd"] = cn
						cn.isUnsorted = true
						VLSDBlock, err := CG.New(file, version, cnBlock.Link.Data)
						if err != nil {
							panic(err)
						}

						VLSD := &ChannelGroup{
							Block:      VLSDBlock,
							Channels:   vsldMap,
							DataGroup:  dataGroup.block,
							SourceInfo: SI.Get(file, version, VLSDBlock.Link.SiAcqSource),
							Comment:    comment,
						}

						UnsortedBlocks.channelGroupsByID[VLSDBlock.Data.RecordId] = VLSD
					}
				}

				channelGroup.Channels[cn.Name] = cn
				m.Channels = append(m.Channels, *cn)
				nextAddressCN = cnBlock.Next()
				dataGroup.ChannelGroup = append(dataGroup.ChannelGroup, channelGroup)
			}
			m.ChannelGroup = append(m.ChannelGroup, *channelGroup)
			nextAddressCG = cgBlock.Next()
		}

		m.DataGroups = append(m.DataGroups, dataGroup)
		if isUnsorted {
			m.UnsortedBlocks = append(m.UnsortedBlocks, UnsortedBlocks)
			m.Sort(UnsortedBlocks)
		}

		nextDataGroupAddress = dataGroup.block.Next()
		dgindex++
	}
}

// Sort is applied for unsorted files.
func (m *MF4) Sort(us UnsortedBlock) error {
	headerID, err := blocks.GetHeaderID(m.File, us.dataGroup.block.Link.Data)
	if err != nil {
		return err
	}

	var (
		addr             int64 = us.dataGroup.block.Link.Data
		dtl              *DL.Block
		offsetDT, target uint64
	)

	isDataList := false
	k := 0
	if headerID == blocks.DlID {
		dtl, err = DL.New(m.File, m.MdfVersion(), addr)
		if err != nil {
			return err
		}

		addr = dtl.Link.Data[k]
		offsetDT = dtl.DataSectionLength(k)
		target = offsetDT
		isDataList = true
		k++
	}

	dtsize, err := blocks.GetLength(m.File, addr)
	if err != nil {
		return err
	}

	pos := 0
	buf := make([]byte, dtsize)
	_, err = io.ReadFull(m.File, buf)
	if err != nil {
		return err
	}

	var (
		byteSize            int
		rowSize             int
		bsize, size, offset uint32
		i, dataBlockSize    uint64
	)

	for uint64(pos) < dtsize {
		if i == target && isDataList {
			// Next list
			if k == len(dtl.Link.Data) && dtl.Next() != 0 {
				dtl, err := DL.New(m.File, m.MdfVersion(), addr)
				if err != nil {
					return err
				}
				addr = dtl.Link.Data[k]
				k = 0
			}

			//Next Data
			offsetDT = dtl.DataSectionLength(k)
			target += offsetDT

			if _, err = m.File.Seek(addr, io.SeekStart); err != nil {
				return err
			}

			dtsize, err = blocks.GetLength(m.File, addr)
			if err != nil {
				return err
			}

			if dtsize != dataBlockSize {
				buf = make([]byte, dtsize)
				dataBlockSize = dtsize
			}

			_, err = io.ReadFull(m.File, buf)
			if err != nil {
				return err
			}
			pos = 0
			k += 1
		}

		byteSize = int(us.dataGroup.block.RecordIDSize())
		id, err := bytesOfRecordIDSize(byteSize, buf[pos:pos+byteSize])
		if err != nil {
			return err
		}

		pos += byteSize

		cg, ok := us.channelGroupsByID[id]
		if !ok {
			return fmt.Errorf(fmt.Sprintf("id %d not found", id))
		}

		if cg.Block.IsVLSD() {
			var sampleLength uint32

			sampleLength = binary.LittleEndian.Uint32(buf[pos : pos+4])
			pos += 4

			cn := cg.Channels["vlsd"]
			size = cn.block.SignalBytesRange()
			value, err := parseSignalMeasure(buf[pos:pos+int(sampleLength)],
				cn.block.ByteOrder(), cn.block.LoadDataType(int(size)),
				int(cn.block.Data.BitOffset), int(cn.block.Data.BitCount))

			if err != nil {
				return err
			}
			pos += int(sampleLength)

			cn.CachedSamples = append(cn.CachedSamples, value)
		} else {
			rowSize = int(cg.Block.Data.DataBytes)

			for _, cn := range cg.Channels {
				if cn.isUnsorted {
					continue
				}

				offset = cn.block.Data.ByteOffset
				bsize = offset + cn.block.Data.BitCount/8
				size = cn.block.SignalBytesRange()

				value, err := parseSignalMeasure(buf[pos+int(offset):pos+int(bsize)],
					cn.block.ByteOrder(), cn.block.LoadDataType(int(size)),
					int(cn.block.Data.BitOffset), int(cn.block.Data.BitCount))
				if err != nil {
					panic(err)
				}
				cn.CachedSamples = append(cn.CachedSamples, value)
			}
			pos += rowSize
		}
	}
	return nil
}

func newUnsortedGroup(dataGroup DataGroup) UnsortedBlock {
	unsortedMap := make(map[uint64]*ChannelGroup, 0)
	return UnsortedBlock{
		dataGroup:         &dataGroup,
		channelGroupsByID: unsortedMap,
	}
}

// GetChannelSample loads sample based DataGroupName and ChannelName
func (m *MF4) GetChannelSample(indexDataGroup int, channelName string) ([]interface{}, error) {
	cgrp := m.ChannelGroup[indexDataGroup]

	// Does channel exist in datagroup?
	cn, ok := cgrp.Channels[channelName]
	if !ok {
		return nil, fmt.Errorf("channel %s doens't exist", channelName)
	}

	// if cn.IsAllValuesInvalid() {
	// 	return nil, fmt.Errorf("channel %s has invalid read", channelName)
	// }

	return cn.Sample()
}

// ListAllChannelsNames returns an slice with all channels from the MF4 file
func (m *MF4) ListAllChannels() []Channel {
	return m.Channels
}

// ListAllChannels returns an slice with all channels from the MF4 file
func (m *MF4) ListAllChannelsNames() []string {
	var n []string
	for _, channel := range m.Channels {
		n = append(n, channel.Name)
	}
	return n
}

// ListAllChannels returns an slice with all channels from the MF4 file
func (m *MF4) ListAllChannelsFromDataGroup(datagroupIndex int) ([]*Channel, error) {
	if len(m.DataGroups) < datagroupIndex {
		return nil, fmt.Errorf("datagroup %d doesn't exist", datagroupIndex)
	}

	var cs []*Channel
	dg := m.DataGroups[datagroupIndex]
	for _, cg := range dg.ChannelGroup {
		for _, c := range cg.Channels {
			cs = append(cs, c)
		}
	}

	return cs, nil
}

// MapAllChannelsNames returns an map with all channels from the MF4 file group
// by data group
func (m *MF4) MapAllChannelsNames() map[int]string {
	mp := make(map[int]string)
	for _, channel := range m.Channels {
		mp[channel.DataGroupIndex] = channel.Name
	}
	return mp
}

// MapAllChannels returns an map with all channels from the MF4 file group
// by data group
func (m *MF4) MapAllChannels() map[int]Channel {
	mp := make(map[int]Channel)
	for _, channel := range m.Channels {
		mp[channel.DataGroupIndex] = channel
	}
	return mp
}

// loadEvents loads and processes events from the given MF4 instance.
// Events are represented by EVBLOCK structures, providing synchronization details.
// The function iterates through the linked list of events, creating EV instances
// and handling event details such as names, comments, and scopes.
// If file has no events or errors occur during EV instance creation, it will
// return `nil`.
func (m *MF4) ListEvents() []*EV.Event {
	if m.getFirstEvent() == 0 {
		return nil
	}

	r := make([]*EV.Event, 0)
	nextEvent := m.getFirstEvent()
	for nextEvent != 0 {
		event, err := EV.New(m.File, m.MdfVersion(), nextEvent)
		if err != nil {
			return nil
		}
		r = append(r, event.Load(m.File))
		nextEvent = event.Next()
	}
	return r
}

func readArrayBlock(file *os.File, addr int64) {
	//debug(file,addr,400)
}

// GetAttachmemts iterates over all AT blocks and return to an array
func (m *MF4) GetAttachments() ([]AT.AttFile, error) {
	return AT.Get(m.File, m.getFirstAttachment())
}

// Saves attachment file input to output path
func (m *MF4) SaveAttachmentTo(attachment AT.AttFile, outputPath string) AT.AttFile {
	return attachment.Save(m.File, outputPath)
}

// GetAttachmemts iterates over all AT blocks and return to an array
func (m *MF4) IsMemoryOptimized() bool {
	return m.ReadOptions.MemoryOptimized
}

// Version method returns the MDF file version
func (m *MF4) Version() string {
	return string(m.Identification.Version[:])
}

// ID method returns the MDF file ID
func (m *MF4) ID() string {
	return string(m.Identification.File[:])
}

// CreatedBy method returns the MDF Program identifier
func (m *MF4) CreatedBy() string {
	return string(m.Identification.Program[:])
}

// VersionNumber method returns the Version number of the MDF format, i.e. 420
func (m *MF4) MdfVersion() uint16 {
	return m.Identification.VersionNumber
}

// isUnfinalized method returns Standard flags for unfinalized MDF
func (m *MF4) IsFinalized() bool {
	return m.Identification.UnfinalizedFlag == 0
}

func (m *MF4) firstDataGroup() int64 {
	return m.Header.Link.DgFirst
}

func (m *MF4) loadFirstFileHistory() {
	m.FileHistory = m.Header.Link.FhFirst
}

func (m *MF4) getFirstAttachment() int64 {
	return m.Header.Link.AtFirst
}

func (m *MF4) getFirstEvent() int64 {
	return m.Header.Link.EvFirst
}

// Start angle in radians at the beginning of the measurement serves as the
// reference point for angle synchronous measurements.
func (m *MF4) StartAngleRad() (float64, error) {
	if !m.isDistanceValid() {
		return 0, fmt.Errorf("start angle rad is not valid for this file")
	}
	return m.getStartAngleRad(), nil
}

// BytesOfRecordIDSize returns number of Bytes used for record IDs in the data
// block.
func bytesOfRecordIDSize(size int, buf []byte) (uint64, error) {
	switch size {
	case 0:
		return 0, nil // Sorted record
	case 1:
		return uint64(buf[0]), nil
	case 2:
		return uint64(binary.LittleEndian.Uint16(buf[:2])), nil
	case 4:
		return uint64(binary.LittleEndian.Uint32(buf[:4])), nil
	case 8:
		return binary.LittleEndian.Uint64(buf[:8]), nil
	default:
		return 0, fmt.Errorf("invalid number of bytes for record IDs: %d", size)
	}
}

// Start distance in meters in meters at the beginning of the measurement serves
// as the reference point for distance synchronous measurements.
func (m *MF4) StartDistanceM() (float64, error) {
	if m.isDistanceValid() {
		return 0, fmt.Errorf("start distance meters is not valid for this file")
	}
	return m.getStartDistanceM(), nil
}

func (m *MF4) GetMeasureComment() string {
	if m.getHeaderMdComment() == 0 {
		return ""
	}

	t, err := TX.GetText(m.File, m.getHeaderMdComment())
	if err != nil {
		return ""
	}

	return t
}

// ReadChangeLog reads and prints the change log entries from the MF4 file.
// The change log is stored in FHBLOCK structures, each representing a change
// made to the MDF file.
// The function iterates through the linked list of FHBLOCKs starting from the
// first one referenced by the HDBLOCK, printing the chronological change history.
//
// Parameters:
//
//	m: A pointer to the MF4 instance containing the file change log.
func (m *MF4) ReadChangeLog() []string {
	r := make([]string, 0)
	nextAddressFH := m.getFileHistory()
	for nextAddressFH != 0 {
		fhBlock, _ := FH.New(m.File, nextAddressFH)

		c := fhBlock.GetChangeLog(m.File)
		t := fhBlock.GetTimeNs()
		f := fhBlock.GetTimeFlag()

		r = append(r, m.formatLog(t, f, c))
		nextAddressFH = fhBlock.Next()
	}
	return r
}

// StartTimeNs returns the start timestamp of measurement in nanoseconds
func (m *MF4) GetStartTimeNs() int64 {
	t := m.getStartTimeNs()
	tzo := uint64(m.getHDTimezoneOffsetMin())
	dlo := uint64(m.getDaylightOffsetMin())
	tf := m.getTimeFlag()
	return m.GetTimeNs(t, tzo, dlo, tf)
}

func (m *MF4) GetStartTimeLT() time.Time {
	return m.formatTimeLT(m.GetStartTimeNs())
}

func (m *MF4) getFileHistory() int64 {
	return m.FileHistory
}

func (m *MF4) formatLog(t int64, f uint8, c string) string {
	ts := m.formatTimeLT(t)
	return fmt.Sprint(ts, f, c)
}

func (m *MF4) getHDTimezoneOffsetMin() int16 {
	return m.Header.Data.TZOffsetMin
}

func (m *MF4) getTimeFlag() uint8 {
	return m.Header.Data.TimeFlags
}

func (m *MF4) getStartTimeNs() uint64 {
	return m.Header.Data.StartTimeNs
}

func (m *MF4) getStartAngleRad() float64 {
	return m.Header.Data.StartAngleRad
}

func (m *MF4) getStartDistanceM() float64 {
	return m.Header.Data.StartDistM
}

func (m *MF4) isDistanceValid() bool {
	return m.Header.Data.Flags == 1
}

func (m *MF4) getTimeClass() uint8 {
	return m.Header.Data.TimeClass
}

func (m *MF4) loadHeader() {
	var err error
	m.Header, err = HD.New(m.File, blocks.IdblockSize)
	if err != nil {
		panic(err)
	}
}

func (m *MF4) getHeaderMdComment() int64 {
	return m.Header.Link.MdComment
}

func seekRead(file *os.File, readAddr int64, data []byte) {
	_, errs := file.Seek(readAddr, 0)
	if errs != nil {
		if errs != io.EOF {
			fmt.Println(errs, "memory Addr out of size")
		}
	}
	_, err := file.Read(data)
	if err != nil {
		if err != io.EOF {
			fmt.Println("loadBuffer error: ", err)
		}
	}
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
