package mwsupply

import "testing"

func TestParseDBRange(t *testing.T) {
	cases := []struct {
		in string
		lo int
		hi int
		ok bool
	}{
		{`{"db_range":[1,15]}`, 1, 15, true},
		{`{"db_range": [0, 7]}`, 0, 7, true}, // PG jsonb::text 带空格
		{`{"default_db":0}`, 0, 0, false},    // 无 db_range（bind_existing 的 isolation）
		{``, 0, 0, false},
		{`not json`, 0, 0, false},
		{`{"db_range":[5]}`, 0, 0, false},   // 长度不对
		{`{"db_range":[5,3]}`, 0, 0, false}, // hi<lo
	}
	for _, c := range cases {
		lo, hi, ok := ParseDBRange(c.in)
		if lo != c.lo || hi != c.hi || ok != c.ok {
			t.Errorf("ParseDBRange(%q)=%d,%d,%v 想 %d,%d,%v", c.in, lo, hi, ok, c.lo, c.hi, c.ok)
		}
	}
}

func TestPickLowestFree(t *testing.T) {
	// 空池 → 1
	if tok, ok := pickLowestFree(1, 15, nil); tok != "1" || !ok {
		t.Fatalf("空池应得 1，得 %q,%v", tok, ok)
	}
	// 占了 1,2 → 3
	if tok, _ := pickLowestFree(1, 15, []string{"1", "2"}); tok != "3" {
		t.Fatalf("占 1,2 应得 3，得 %q", tok)
	}
	// 回收 1（不在 allocated）→ 复用 1
	if tok, _ := pickLowestFree(1, 15, []string{"2", "3"}); tok != "1" {
		t.Fatalf("1 空闲应复用 1，得 %q", tok)
	}
	// 全占 → false
	if _, ok := pickLowestFree(1, 3, []string{"1", "2", "3"}); ok {
		t.Fatal("全占应 false")
	}
	// allocated 含 1（string）不漏
	if tok, _ := pickLowestFree(1, 2, []string{"1"}); tok != "2" {
		t.Fatalf("占 1 应得 2，得 %q", tok)
	}
}
