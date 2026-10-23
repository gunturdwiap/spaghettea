package main

import (
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strconv"
)

type actionType string

const (
	chatAction   actionType = "chat"
	typingAction actionType = "typing"
	errorAction  actionType = "error"
)

func (at *actionType) UnmarshalJSON(raw []byte) error {
	unquoted, err := strconv.Unquote(string(raw))
	if err != nil {
		return errors.New("invalid action type")
	}

	parsed, err := parseActionType(unquoted)
	if err != nil {
		return err
	}

	*at = parsed

	return nil
}

func parseActionType(raw string) (actionType, error) {
	switch at := actionType(raw); at {
	case chatAction, typingAction:
		return at, nil
	default:
		return "", fmt.Errorf("invalid action type: %q", raw)
	}
}

type action struct {
	Type    actionType     `json:"type"`
	Payload jsontext.Value `json:"payload"`
}

func marshalAction(at actionType, v any) ([]byte, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	return json.Marshal(action{
		Type:    at,
		Payload: payload,
	})
}
