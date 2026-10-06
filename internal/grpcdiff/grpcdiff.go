// Package grpcdiff structurally diffs two protobuf messages of the same
// type using their descriptors (reflection), reusing internal/diff's
// Change/severity/clustering machinery so gRPC and HTTP/JSON diffs are
// reported identically. Scoped to scalar and nested-message fields for
// v1; see docs/limits.md for repeated/map field handling.
package grpcdiff

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/jex2l/parity/internal/diff"
)

// Compare structurally diffs before and after, which must be the same
// proto message type. Produces diff.Change values at "$.<field>"-style
// paths so they're directly comparable/clusterable alongside JSON diffs.
func Compare(before, after proto.Message) []diff.Change {
	var changes []diff.Change
	compareMessages("$", before.ProtoReflect(), after.ProtoReflect(), &changes)
	return changes
}

func compareMessages(path string, before, after protoreflect.Message, changes *[]diff.Change) {
	fields := before.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		childPath := fmt.Sprintf("%s.%s", path, fd.Name())

		// Nested messages have real presence tracking in proto3 (a submessage
		// field is either set or not); plain scalar fields do not (a proto3
		// scalar with no "optional" keyword can't be distinguished from "set
		// to the zero value"), so Has() is only meaningful for the former.
		// For scalars we just compare values directly — a field "going back
		// to its zero value" and "never being set" are indistinguishable at
		// the wire level, which is an inherent proto3 limitation, not a bug
		// in this diff (documented in docs/limits.md).
		if fd.Kind() == protoreflect.MessageKind && !fd.IsList() && !fd.IsMap() {
			bSet, aSet := before.Has(fd), after.Has(fd)
			switch {
			case bSet && !aSet:
				*changes = append(*changes, diff.Change{Path: childPath, Type: diff.FieldRemoved})
			case !bSet && aSet:
				*changes = append(*changes, diff.Change{Path: childPath, Type: diff.FieldAdded})
			case bSet && aSet:
				compareMessages(childPath, before.Get(fd).Message(), after.Get(fd).Message(), changes)
			}
			continue
		}

		bv, av := valueOf(before, fd), valueOf(after, fd)
		if bv != av {
			*changes = append(*changes, diff.Change{Path: childPath, Type: diff.ValueChanged, Before: bv, After: av})
		}
	}
}

// valueOf renders a scalar/enum field value as a comparable Go value.
// Repeated and map fields are rendered as their count only — see
// docs/limits.md for why full element-wise diffing isn't implemented yet.
func valueOf(m protoreflect.Message, fd protoreflect.FieldDescriptor) any {
	v := m.Get(fd)
	if fd.IsList() {
		return fmt.Sprintf("<list len=%d>", v.List().Len())
	}
	if fd.IsMap() {
		return fmt.Sprintf("<map len=%d>", v.Map().Len())
	}
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return v.Bool()
	case protoreflect.StringKind:
		return v.String()
	case protoreflect.BytesKind:
		return string(v.Bytes())
	case protoreflect.EnumKind:
		return int64(v.Enum())
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return v.Float()
	case protoreflect.Int32Kind, protoreflect.Int64Kind, protoreflect.Sint32Kind, protoreflect.Sint64Kind,
		protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind:
		return v.Int()
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind, protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
		return v.Uint()
	default:
		return fmt.Sprintf("%v", v.Interface())
	}
}
