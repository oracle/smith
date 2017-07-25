package main

import "testing"

type parseCase struct {
	User string
	Uid  int
	Gid  int
	U    string
	G    string
	Nss  bool
}

func TestParseUser(t *testing.T) {
	for _, c := range []parseCase{
		{"root:root", 0, 0, "root", "root", true},
		{"0:0", 0, 0, "root", "root", false},
		{"daemon:daemon", 1, 1, "daemon", "daemon", true},
		{"1:1", 1, 1, "daemon", "daemon", false},
		{"smith:0", 10, 0, "smith", "root", true},
		{"0:smith", 0, 10, "root", "smith", true},
		{"1000:1000", 1000, 1000, "smith", "smith", false},
		{"foo:bar", 10, 10, "foo", "bar", true},
		{"foo:1000", 10, 1000, "foo", "smith", true},
		{"1000:bar", 1000, 10, "smith", "bar", true},
		{"", 10, 10, "smith", "smith", false},
	} {
		uid, gid, u, g, nss := ParseUser(c.User)
		if uid != c.Uid {
			t.Fatalf("Fail %v, uids don't match: %d != %d", c, uid, c.Uid)
		}
		if gid != c.Gid {
			t.Fatalf("Fail %v, gids don't match: %d != %d", c, gid, c.Gid)
		}
		if u != c.U {
			t.Fatalf("Fail %v, users don't match: %s != %s", c, u, c.U)
		}
		if g != c.G {
			t.Fatalf("Fail %v, groups don't match: %s != %s", c, g, c.G)
		}
		if nss != c.Nss {
			t.Fatalf("Fail %v, nss doesn't match: %t != %t", c, nss, c.Nss)
		}
	}
}
