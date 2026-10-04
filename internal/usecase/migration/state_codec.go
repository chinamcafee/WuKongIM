package migration

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
)

const stateEncoding = "wkmigrate-bytes-v1"

// MarshalState serializes internal migration state deterministically without
// coercing Go string bytes to UTF-8. Source archives and public plans/reports
// retain their own wire formats. Raw JSON and standard JSON-marshaling types
// keep their existing encoding; all ordinary string values and map keys use
// base64 inside this versioned envelope. []byte payloads are not copied.
func MarshalState(value any) ([]byte, error) {
	v, err := stateStrings(reflect.ValueOf(value), false, 0)
	if err != nil {
		return nil, err
	}
	var transformed any
	if v.IsValid() {
		transformed = v.Interface()
	}
	return json.Marshal(struct {
		Encoding string `json:"encoding"`
		Value    any    `json:"value"`
	}{stateEncoding, transformed})
}

// UnmarshalState restores exact string bytes. Unversioned workspaces are
// rejected: replacing malformed bytes or accepting another codec could change
// source identities, routing, or the independent verification result.
func UnmarshalState(data []byte, value any) error {
	var envelope struct {
		Encoding string          `json:"encoding"`
		Value    json.RawMessage `json:"value"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&envelope); err != nil {
		return fmt.Errorf("invalid migration state envelope: %w", err)
	}
	if d.Decode(new(any)) != io.EOF || envelope.Encoding != stateEncoding || len(envelope.Value) == 0 {
		return errors.New("unsupported migration state encoding; use a fresh workspace and matching archive")
	}
	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return errors.New("migration state requires a non-nil destination pointer")
	}
	// Decode into an isolated destination so malformed base64 cannot partially
	// mutate a value retained by the caller.
	target := reflect.New(v.Elem().Type())
	if err := json.Unmarshal(envelope.Value, target.Interface()); err != nil {
		return err
	}
	restored, err := stateStrings(target.Elem(), true, 0)
	if err != nil {
		return err
	}
	v.Elem().Set(restored)
	return nil
}

var jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
var jsonNumberType = reflect.TypeOf(json.Number(""))

func stateStrings(v reflect.Value, decode bool, depth int) (reflect.Value, error) {
	if depth > 64 {
		return reflect.Value{}, errors.New("migration state nesting limit exceeded")
	}
	if !v.IsValid() {
		return v, nil
	}
	if v.Type() == jsonNumberType || v.Type().Implements(jsonMarshalerType) {
		return v, nil
	}
	switch v.Kind() {
	case reflect.String:
		s := v.String()
		if decode {
			raw, err := base64.StdEncoding.Strict().DecodeString(s)
			if err != nil || base64.StdEncoding.EncodeToString(raw) != s {
				return reflect.Value{}, errors.New("invalid migration state string encoding")
			}
			s = string(raw)
		} else {
			s = base64.StdEncoding.EncodeToString([]byte(s))
		}
		out := reflect.New(v.Type()).Elem()
		out.SetString(s)
		return out, nil
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return v, nil
		}
		item, err := stateStrings(v.Elem(), decode, depth+1)
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(v.Type()).Elem()
		if v.Kind() == reflect.Pointer {
			out.Set(reflect.New(v.Type().Elem()))
			out.Elem().Set(item)
		} else {
			out.Set(item)
		}
		return out, nil
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath != "" {
				continue
			}
			field, err := stateStrings(v.Field(i), decode, depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Field(i).Set(field)
		}
		return out, nil
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return v, nil
		}
		if v.Kind() == reflect.Slice && v.IsNil() {
			return v, nil
		}
		var out reflect.Value
		if v.Kind() == reflect.Slice {
			out = reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		} else {
			out = reflect.New(v.Type()).Elem()
		}
		for i := 0; i < v.Len(); i++ {
			item, err := stateStrings(v.Index(i), decode, depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(item)
		}
		return out, nil
	case reflect.Map:
		if v.IsNil() {
			return v, nil
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			key, err := stateStrings(iter.Key(), decode, depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			item, err := stateStrings(iter.Value(), decode, depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.SetMapIndex(key, item)
		}
		return out, nil
	default:
		return v, nil
	}
}

// IdentityKey encodes a typed logical key without collapsing distinct invalid
// UTF-8 byte sequences or treating a source identifier as a physical DB key.
func IdentityKey(values ...any) string {
	data, err := MarshalState(values)
	if err != nil {
		panic("unsupported internal migration identity key")
	}
	return base64.RawURLEncoding.EncodeToString(data)
}
