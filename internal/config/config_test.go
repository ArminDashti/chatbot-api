package config

import "testing"

func TestResolveChatBaseURLHostUnchanged(t *testing.T) {
	in := "http://127.0.0.1:8130/v1"
	if got := resolveChatBaseURL(in, false); got != in {
		t.Fatalf("host: got %q want %q", got, in)
	}
}

func TestResolveChatBaseURLContainerLoopback(t *testing.T) {
	got := resolveChatBaseURL("http://127.0.0.1:8130/v1", true)
	want := "http://host.docker.internal:8130/v1"
	if got != want {
		t.Fatalf("container loopback: got %q want %q", got, want)
	}
}

func TestResolveChatBaseURLContainerKeepsRemote(t *testing.T) {
	in := "https://api.cursor.com/v1"
	if got := resolveChatBaseURL(in, true); got != in {
		t.Fatalf("remote: got %q want %q", got, in)
	}
}
