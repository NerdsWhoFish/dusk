// Package tokens measures response text without a network call or model inference.
package tokens

import (
	"sync"

	"github.com/tiktoken-go/tokenizer/codec"
)

// Encoding identifies the vocabulary used for response-size estimates.
const Encoding = "o200k_base"

var encoder = sync.OnceValue(codec.NewO200kBase)

// Count measures ordinary text with an embedded vocabulary, without model or
// protocol overhead. Other model encodings can produce different counts.
func Count(text string) (int, error) {
	return encoder().Count(text)
}
