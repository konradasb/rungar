// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"encoding/json"
	"io"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// jsonOptions marshal messages with their proto field names.
var jsonOptions = protojson.MarshalOptions{UseProtoNames: true}

// writeJSON writes a message as indented JSON.
func writeJSON(w io.Writer, msg proto.Message) error {
	b, err := jsonOptions.Marshal(msg)
	if err != nil {
		return err
	}

	return writeIndented(w, json.RawMessage(b))
}

// writeJSONObject writes msgs as one indented JSON object, keyed as given.
func writeJSONObject(w io.Writer, msgs map[string]proto.Message) error {
	obj := make(map[string]json.RawMessage, len(msgs))
	for key, msg := range msgs {
		b, err := jsonOptions.Marshal(msg)
		if err != nil {
			return err
		}
		obj[key] = b
	}

	return writeIndented(w, obj)
}

func writeIndented(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(v)
}
