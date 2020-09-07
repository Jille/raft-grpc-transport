package transport

import "encoding/json"

// TODO(quis): Don't use JSON.

func encode(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func decode(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
