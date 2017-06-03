package main

import (
	"io"
	"time"
	"unsafe"
)

var (
	_ = unsafe.Sizeof(0)
	_ = io.ReadFull
	_ = time.Now()
)

type Thumb struct {
	Created time.Time
	Width   int32
	Height  int32
	Mime    string
	Data    []byte
}

func (d *Thumb) Size() (s uint64) {

	{
		l := uint64(len(d.Mime))

		{

			t := l
			for t >= 0x80 {
				t >>= 7
				s++
			}
			s++

		}
		s += l
	}
	{
		l := uint64(len(d.Data))

		{

			t := l
			for t >= 0x80 {
				t >>= 7
				s++
			}
			s++

		}
		s += l
	}
	s += 23
	return
}
func (d *Thumb) Marshal(buf []byte) ([]byte, error) {
	size := d.Size()
	{
		if uint64(cap(buf)) >= size {
			buf = buf[:size]
		} else {
			buf = make([]byte, size)
		}
	}
	i := uint64(0)

	{
		b, err := d.Created.MarshalBinary()
		if err != nil {
			return nil, err
		}
		copy(buf[0:], b)
	}
	{

		buf[0+15] = byte(d.Width >> 0)

		buf[1+15] = byte(d.Width >> 8)

		buf[2+15] = byte(d.Width >> 16)

		buf[3+15] = byte(d.Width >> 24)

	}
	{

		buf[0+19] = byte(d.Height >> 0)

		buf[1+19] = byte(d.Height >> 8)

		buf[2+19] = byte(d.Height >> 16)

		buf[3+19] = byte(d.Height >> 24)

	}
	{
		l := uint64(len(d.Mime))

		{

			t := uint64(l)

			for t >= 0x80 {
				buf[i+23] = byte(t) | 0x80
				t >>= 7
				i++
			}
			buf[i+23] = byte(t)
			i++

		}
		copy(buf[i+23:], d.Mime)
		i += l
	}
	{
		l := uint64(len(d.Data))

		{

			t := uint64(l)

			for t >= 0x80 {
				buf[i+23] = byte(t) | 0x80
				t >>= 7
				i++
			}
			buf[i+23] = byte(t)
			i++

		}
		copy(buf[i+23:], d.Data)
		i += l
	}
	return buf[:i+23], nil
}

func (d *Thumb) Unmarshal(buf []byte) (uint64, error) {
	i := uint64(0)

	{
		d.Created.UnmarshalBinary(buf[i+0 : i+0+15])
	}
	{

		d.Width = 0 | (int32(buf[i+0+15]) << 0) | (int32(buf[i+1+15]) << 8) | (int32(buf[i+2+15]) << 16) | (int32(buf[i+3+15]) << 24)

	}
	{

		d.Height = 0 | (int32(buf[i+0+19]) << 0) | (int32(buf[i+1+19]) << 8) | (int32(buf[i+2+19]) << 16) | (int32(buf[i+3+19]) << 24)

	}
	{
		l := uint64(0)

		{

			bs := uint8(7)
			t := uint64(buf[i+23] & 0x7F)
			for buf[i+23]&0x80 == 0x80 {
				i++
				t |= uint64(buf[i+23]&0x7F) << bs
				bs += 7
			}
			i++

			l = t

		}
		d.Mime = string(buf[i+23 : i+23+l])
		i += l
	}
	{
		l := uint64(0)

		{

			bs := uint8(7)
			t := uint64(buf[i+23] & 0x7F)
			for buf[i+23]&0x80 == 0x80 {
				i++
				t |= uint64(buf[i+23]&0x7F) << bs
				bs += 7
			}
			i++

			l = t

		}
		if uint64(cap(d.Data)) >= l {
			d.Data = d.Data[:l]
		} else {
			d.Data = make([]byte, l)
		}
		copy(d.Data, buf[i+23:])
		i += l
	}
	return i + 23, nil
}
