package presentation

import "testing"

func TestThemeContrastAndStrictColorTokens(t *testing.T) {
	valid := Theme{Light: AccentPair{Accent: "#00675f", Foreground: "#ffffff"}, Dark: AccentPair{Accent: "#78d9cb", Foreground: "#14141d"}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	current := Theme{Light: AccentPair{Accent: "#6242d2", Foreground: "#ffffff"}, Dark: AccentPair{Accent: "#b39aff", Foreground: "#14141d"}}
	if err := current.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []AccentPair{{"red", "#ffffff"}, {"#fff", "#000000"}, {"#00675f;url(x)", "#ffffff"}, {"#ffffff", "#000000"}, {"#00675f", "#00675f"}} {
		bad := valid
		bad.Light = pair
		if bad.Validate() == nil {
			t.Fatalf("unsafe theme accepted: %+v", pair)
		}
	}
	bad := valid
	bad.Dark = AccentPair{Accent: "#14141d", Foreground: "#ffffff"}
	if bad.Validate() == nil {
		t.Fatal("unreadable dark links accepted")
	}
}
