package recording

import (
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func marshalJSON(m proto.Message) ([]byte, error) {
	return protojson.Marshal(m)
}
