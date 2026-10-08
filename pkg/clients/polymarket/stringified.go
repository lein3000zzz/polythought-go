package polymarket

import "encoding/json"

// StringifiedSlice decodes Gamma fields that hold a JSON array encoded as a string,
// like outcomes: "[\"Yes\", \"No\"]". A plain array is accepted too.
type StringifiedSlice []string

func (s *StringifiedSlice) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}

	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return json.Unmarshal(b, (*[]string)(s))
	}

	if raw == "" {
		return nil
	}

	return json.Unmarshal([]byte(raw), (*[]string)(s))
}
