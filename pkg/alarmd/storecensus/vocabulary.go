// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storecensus

import (
	"regexp"
	"strings"
)

// Vocabulary is the words a key family keeps: the words the code that writes
// a store builds its keys from. A family name is a metric label, and a key
// carries whatever its writer put in it - a receiver's name, a strategy's
// name, a mail address, text in any language. No test on the characters of
// a word tells a receiver named in lower case from a word of kind, so the
// test is the other way round: a word is kept only when it is known to be
// the code's, and every other word is written "*".
//
// Its sources are the code, never the store: alarmd's own key literals
// (words_alarmd.go, generated from this module's source), the platform's
// (words_platform.go, from its alarm backend's key literals), the task
// queue's, and the key prefixes this deployment is configured with. A word
// of the code missing from them names its keys' families less exactly; it
// never lets a value through. A value that is spelled only in words of the
// code - a receiver named "admin" - is indistinguishable from them and
// keeps them, which says nothing the code does not.
type Vocabulary struct {
	words map[string]struct{}
	// segments are whole segments of the configured prefixes, kept as they
	// are however they are spelled: a prefix is the deployment's, not data.
	segments map[string]struct{}
}

// frameworkWords are the task queue's and its broker's own key words, which
// share the platform's stores.
var frameworkWords = []string{"binding", "celery", "celeryev", "kombu", "meta", "reply", "task", "unacked", "index", "mutex"}

var defaultVocabulary = NewVocabulary()

// NewVocabulary is the built-in words and those of the configured key
// prefixes given.
func NewVocabulary(prefixes ...string) *Vocabulary {
	vocabulary := &Vocabulary{words: map[string]struct{}{}, segments: map[string]struct{}{}}
	for _, list := range [][]string{alarmdWords, platformWords, frameworkWords} {
		for _, word := range list {
			vocabulary.words[word] = struct{}{}
		}
	}
	for _, prefix := range prefixes {
		for _, segment := range strings.FieldsFunc(prefix, func(r rune) bool { return r == ':' || r == '.' }) {
			vocabulary.segments[segment] = struct{}{}
			for _, word := range wordsOf(segment) {
				vocabulary.words[word] = struct{}{}
			}
		}
	}
	return vocabulary
}

// knows reports a word of the code.
func (vocabulary *Vocabulary) knows(word string) bool {
	_, known := vocabulary.words[word]
	return known
}

var formatVerb = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z]`)

// wordsOf is the words of a lower-case key literal: its runs of letters and
// digits that begin with a letter, format verbs taken out, instances left
// out. A literal with an upper-case letter is not a key's and has none.
func wordsOf(literal string) []string {
	if strings.ToLower(literal) != literal {
		return nil
	}
	var words []string
	for _, word := range strings.FieldsFunc(formatVerb.ReplaceAllString(literal, " "), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if word[0] >= 'a' && word[0] <= 'z' && !instance(word) {
			words = append(words, word)
		}
	}
	return words
}
