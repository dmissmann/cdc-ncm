package ncm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
)

type header struct {
	Signature   uint32
	HeaderLen   uint16
	SequenceNum uint16
	BlockLen    uint16
	NpdIndex    uint16
}

func (h header) String() string {
	return fmt.Sprintf("Header[len=%d, seq=%d, blockLen=%d]", h.HeaderLen, h.SequenceNum, h.BlockLen)
}

type datagramPointerHeader struct {
	Signature    uint32
	Length       uint16
	NextNpdIndex uint16
}

func (d datagramPointerHeader) String() string {
	return fmt.Sprintf("DatagramPointerHeader[len=%d, nextNdp=%d]", d.Length, d.NextNpdIndex)
}

type datagram struct {
	Index  uint16
	Length uint16
}

type NcmWrapper struct {
	targetReader io.Reader
	targetWriter io.Writer
	buf          *bytes.Buffer
	sequenceNum  uint16
}

const headerSignature = 0x484D434E
const datagramPointerHeaderSignature = 0x304D434E

func NewWrapper(targetReader io.Reader, targetWriter io.Writer) *NcmWrapper {
	return &NcmWrapper{
		targetReader: targetReader,
		targetWriter: targetWriter,
		buf:          bytes.NewBuffer(nil),
		sequenceNum:  0,
	}
}

func (r *NcmWrapper) Read(p []byte) (int, error) {
	if r.buf.Len() >= len(p) {
		return r.buf.Read(p)
	}
	var h header
	err := binary.Read(r.targetReader, binary.LittleEndian, &h)
	if err != nil {
		return 0, err
	}
	if h.Signature != headerSignature {
		return 0, fmt.Errorf("wrong header signature")
	}

	var dh datagramPointerHeader
	err = binary.Read(r.targetReader, binary.LittleEndian, &dh)
	if err != nil {
		return 0, err
	}
	if dh.Signature != datagramPointerHeaderSignature {
		return 0, fmt.Errorf("wrong datagram pointer signature")
	}

	if dh.Length == 0x8c {
		slog.Warn("change datagram size")
		dh.Length = 0x3c
	}

	slog.Info("Frame read", slog.String("header", h.String()), (slog.String("dgP", dh.String())))

	datagrams := make([]byte, dh.Length-8)
	_, err = r.targetReader.Read(datagrams)
	if err != nil {
		return 0, err
	}
	skipped, err := io.CopyN(io.Discard, r.targetReader, 2)
	if err != nil {
		return 0, err
	}
	if skipped != 2 {
		return 0, fmt.Errorf("could not skip 2 bytes")
	}

	totalHeaderLength := h.HeaderLen + dh.Length + 2
	payloadLength := int(h.BlockLen - totalHeaderLength)

	payload := make([]byte, h.BlockLen-totalHeaderLength)
	n, err := r.targetReader.Read(payload)
	if err != nil {
		return 0, err
	}
	if n != payloadLength {
		return 0, fmt.Errorf("expected %d bytes, but only read %d", payloadLength, n)
	}

	outOffset := 0

	datagramReader := bytes.NewReader(datagrams)
	for i := 0; i < len(datagrams)/4; i++ {
		var d datagram
		err = binary.Read(datagramReader, binary.LittleEndian, &d)
		if err != nil {
			return 0, err
		}
		if d.Length == 0 {
			continue
		}
		offset := d.Index - totalHeaderLength
		slog.Info("read datagram", slog.Int64("offset", int64(offset)), slog.Int64("len", int64(d.Length)))
		_, err = r.buf.Write(payload[offset:d.Length])
		if err != nil {
			return 0, fmt.Errorf("could not copy datagram into buffer. %w", err)
		}
		outOffset += int(d.Length)
	}

	return r.buf.Read(p)
	//return outOffset, nil
}

func (r *NcmWrapper) Write(p []byte) (n int, err error) {

	h := header{
		Signature:   headerSignature,
		HeaderLen:   12,
		SequenceNum: r.sequenceNum,
		BlockLen:    uint16(len(p) + 12 + 8 + 8 + 2),
		NpdIndex:    12,
	}

	dh := datagramPointerHeader{
		Signature:    datagramPointerHeaderSignature,
		Length:       16,
		NextNpdIndex: 0,
	}

	r.sequenceNum++

	buf := bytes.NewBuffer(nil)

	binary.Write(buf, binary.LittleEndian, h)
	binary.Write(buf, binary.LittleEndian, dh)

	d := datagram{
		Index:  30,
		Length: uint16(len(p)),
	}
	d0 := datagram{
		Index:  0,
		Length: 0,
	}
	binary.Write(buf, binary.LittleEndian, d)
	binary.Write(buf, binary.LittleEndian, d0)
	buf.WriteByte(0)
	buf.WriteByte(0)
	buf.Write(p)
	r.targetWriter.Write(buf.Bytes())
	return len(p), err
}
