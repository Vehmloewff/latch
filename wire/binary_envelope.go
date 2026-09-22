package wire

import "unicode/utf8"

// MarshalBinary is the compact transport envelope. The first byte is the
// protocol version, followed by a numeric frame kind and only the fields used
// by that frame. Strings and payloads are length-prefixed.
func (e Envelope) MarshalBinary() ([]byte, error) {
	if err := validateEnvelope(e); err != nil {
		return nil, err
	}

	codes := map[FrameType]byte{
		FrameConnect:         1,
		FrameConnected:       2,
		FrameRequest:         3,
		FrameResponse:        4,
		FrameError:           5,
		FrameEvent:           6,
		FrameConnectionError: 7,
	}
	code := codes[e.Type]

	var x encoder
	x.byte(1)
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
	version, err := d.byte()
	if err != nil || version != 1 {
		return ErrMalformed
	}
	code, err := d.byte()
	if err != nil {
		return err
	}
	codes := []FrameType{"", FrameConnect, FrameConnected, FrameRequest, FrameResponse, FrameError, FrameEvent, FrameConnectionError}
	if code == 0 || int(code) >= len(codes) {
		return ErrMalformed
	}

	parsed := Envelope{Type: codes[code]}
	readString := func() (string, error) {
		x, err := d.stringBlob()
		if err != nil {
			return "", err
		}
		return string(x), nil
	}
	if parsed.Version, err = readString(); err != nil {
		return err
	}
	if parsed.ID, err = readString(); err != nil {
		return err
	}
	if parsed.Method, err = readString(); err != nil {
		return err
	}
	if parsed.Event, err = readString(); err != nil {
		return err
	}
	if parsed.Payload, err = d.blob(); err != nil {
		return err
	}
	if parsed.Error, err = readString(); err != nil {
		return err
	}
	if parsed.ErrorCode, err = readString(); err != nil {
		return err
	}
	if d.pos != len(data) {
		return ErrMalformed
	}
	if err := validateEnvelope(parsed); err != nil {
		return err
	}
	*e = parsed
	return nil
}

func validateEnvelope(e Envelope) error {
	if !utf8.ValidString(string(e.Type)) ||
		!utf8.ValidString(e.Version) ||
		!utf8.ValidString(e.ID) ||
		!utf8.ValidString(e.Method) ||
		!utf8.ValidString(e.Event) ||
		!utf8.ValidString(e.Error) ||
		!utf8.ValidString(e.ErrorCode) {
		return ErrMalformed
	}

	switch e.Type {
	case FrameConnect, FrameConnected:
		if e.Version == "" {
			return ErrMalformed
		}
	case FrameRequest:
		if e.ID == "" || e.Method == "" {
			return ErrMalformed
		}
	case FrameResponse:
		if e.ID == "" {
			return ErrMalformed
		}
	case FrameError:
		if e.ID == "" || e.ErrorCode == "" {
			return ErrMalformed
		}
	case FrameEvent:
		if len(e.Payload) == 0 {
			return ErrMalformed
		}
	case FrameConnectionError:
		if e.ErrorCode == "" {
			return ErrMalformed
		}
	default:
		return ErrMalformed
	}
	return nil
}
