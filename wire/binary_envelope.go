package wire

// MarshalBinary is the compact transport envelope. The first byte is the
// protocol version, followed by a numeric frame kind and only the fields used
// by that frame. Strings and payloads are length-prefixed.
func (e Envelope) MarshalBinary() ([]byte, error) {
	var x encoder
	x.byte(1)
	codes := map[FrameType]byte{FrameConnect: 1, FrameConnected: 2, FrameRequest: 3, FrameResponse: 4, FrameError: 5, FrameEvent: 6, FrameConnectionError: 7}
	code, ok := codes[e.Type]
	if !ok {
		return nil, ErrMalformed
	}
	x.byte(code)
	x.blob([]byte(e.Version))
	x.blob([]byte(e.ID))
	x.blob([]byte(e.Method))
	x.blob([]byte(e.Event))
	x.blob(e.Payload)
	x.blob([]byte(e.Error))
	x.blob([]byte(e.ErrorCode))
	return x.b, nil
}

func (e *Envelope) UnmarshalBinary(data []byte) error {
	if e == nil {
		return ErrMalformed
	}
	d := decoder{b: data}
	v, err := d.byte()
	if err != nil || v != 1 {
		return ErrMalformed
	}
	code, err := d.byte()
	if err != nil {
		return err
	}
	codes := []FrameType{"", FrameConnect, FrameConnected, FrameRequest, FrameResponse, FrameError, FrameEvent, FrameConnectionError}
	if int(code) >= len(codes) || code == 0 {
		return ErrMalformed
	}
	e.Type = codes[code]
	read := func() ([]byte, error) { return d.blob() }
	var x []byte
	if x, err = read(); err != nil {
		return err
	}
	e.Version = string(x)
	if x, err = read(); err != nil {
		return err
	}
	e.ID = string(x)
	if x, err = read(); err != nil {
		return err
	}
	e.Method = string(x)
	if x, err = read(); err != nil {
		return err
	}
	e.Event = string(x)
	if e.Payload, err = read(); err != nil {
		return err
	}
	if x, err = read(); err != nil {
		return err
	}
	e.Error = string(x)
	if x, err = read(); err != nil {
		return err
	}
	e.ErrorCode = string(x)
	if d.pos != len(data) {
		return ErrMalformed
	}
	return nil
}
