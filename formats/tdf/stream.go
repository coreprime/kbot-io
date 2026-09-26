package tdf

import (
	"bufio"
	"fmt"
	"io"
	"reflect"
)

// Encoder writes TDF documents straight to an io.Writer. Unlike Marshal, which
// builds the whole document in memory before returning bytes, a slice target is
// emitted one top-level [section] at a time, so peak memory is bounded by the
// largest single section rather than the full document.
type Encoder struct {
	w *bufio.Writer
}

// NewEncoder returns an Encoder that writes to w.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: bufio.NewWriter(w)}
}

// Encode renders v as TDF text to the underlying writer and flushes. v must be
// a struct, a slice of structs, or a pointer to either, exactly as for Marshal.
func (e *Encoder) Encode(v any) error {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return fmt.Errorf("tdf: Encode of nil %T", v)
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Slice:
		for i := 0; i < rv.Len(); i++ {
			el, err := encodeElement(rv.Index(i))
			if err != nil {
				return err
			}
			if err := writeElems(e.w, []*element{el}, 0); err != nil {
				return err
			}
		}
	case reflect.Struct:
		children, err := encodeStruct(rv)
		if err != nil {
			return err
		}
		if err := writeElems(e.w, children, 0); err != nil {
			return err
		}
	default:
		return fmt.Errorf("tdf: Encode requires a struct or slice, got %s", rv.Kind())
	}
	return e.w.Flush()
}

// Decoder reads a TDF document from an io.Reader. A slice target is filled one
// top-level [section] at a time as the reader is consumed, so the whole input
// never has to be held in memory the way Unmarshal holds it. It reads exactly
// as Unmarshal does.
type Decoder struct {
	r    *bufio.Reader
	opts ParseOptions
}

// NewDecoder returns a Decoder that reads from r.
func NewDecoder(r io.Reader) *Decoder {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return &Decoder{r: br}
}

// SetOptions sets the parse options used by Decode.
func (d *Decoder) SetOptions(opts ParseOptions) {
	d.opts = opts
}

// Decode parses the document into v, which must be a non-nil pointer to a struct
// or to a slice of structs, exactly as for Unmarshal. A struct target gathers
// every top-level element before decoding (fields may appear in any order); a
// slice target decodes each section as it is read.
func (d *Decoder) Decode(v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("tdf: Decode requires a non-nil pointer, got %T", v)
	}
	target := rv.Elem()
	if k := target.Kind(); k != reflect.Slice && k != reflect.Struct {
		return fmt.Errorf("tdf: Decode target must point to a struct or slice, got %s", k)
	}
	p, err := newParser(d.r, d.opts)
	if err != nil {
		return err
	}
	if target.Kind() == reflect.Struct {
		els, err := p.all()
		if err != nil {
			return err
		}
		return decodeStruct(els, target)
	}
	for {
		el, err := p.next()
		if err != nil {
			return err
		}
		if el == nil {
			return nil
		}
		if !el.section {
			continue
		}
		if err := appendRepeated(el, target); err != nil {
			return err
		}
	}
}
