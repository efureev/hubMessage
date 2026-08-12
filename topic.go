package msghub

import "reflect"

// Topic identifies a stream of events of type T.
//
// A Topic is an ordinary comparable value, built from a plain string, so a
// program can address a stream whose name it computes at run time and can pass
// topics around like any other value. Two topics are the same stream when both
// their name and their type match: [NewTopic] with the same name but a
// different T yields an independent stream, not a conflict.
//
// The zero Topic is not usable; build one with [NewTopic] or [TypeTopic].
type Topic[T any] struct {
	key topicKey
}

// topicKey is what the hub actually indexes by. It is computed once, when the
// topic is built, so neither Publish nor Subscribe pays for reflection.
type topicKey struct {
	name string
	typ  reflect.Type
}

// String renders a key as it appears in errors and logs.
func (k topicKey) String() string {
	if k.name == "" {
		return k.typ.String()
	}

	return k.name + "[" + k.typ.String() + "]"
}

// NewTopic returns the topic named name carrying events of type T.
//
// The name is free-form and may be computed at run time. An empty name is
// legal and yields the same topic as [TypeTopic].
func NewTopic[T any](name string) Topic[T] {
	return Topic[T]{key: topicKey{name: name, typ: reflect.TypeFor[T]()}}
}

// TypeTopic returns the topic that carries events of type T and is identified
// by that type alone.
//
// Use it when the event type is already the whole meaning of the stream — a
// UserCreated is a UserCreated — and naming it would only add a string to keep
// in sync. It is exactly [NewTopic] with an empty name.
func TypeTopic[T any]() Topic[T] {
	return NewTopic[T]("")
}

// Name returns the topic name, which is empty for a [TypeTopic].
func (t Topic[T]) Name() string { return t.key.name }

// String implements [fmt.Stringer], rendering the topic as name[type], or as
// the bare type when the topic has no name.
func (t Topic[T]) String() string {
	if t.key.typ == nil {
		return "msghub.Topic(invalid)"
	}

	return t.key.String()
}

// valid reports whether the topic was built by a constructor rather than being
// a zero value.
func (t Topic[T]) valid() bool { return t.key.typ != nil }
