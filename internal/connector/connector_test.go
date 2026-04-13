package connector

import "testing"

func TestVFSName_IllegalChars(t *testing.T) {
	tests := []struct {
		name string
		item MediaItem
		want string
	}{
		{
			name: "slash in title (Superman/Batman)",
			item: MediaItem{Name: "Superman/Batman: Apocalypse (2010) [imdb-tt1210166]", IsFolder: true},
			want: "Superman∕Batman： Apocalypse (2010) [imdb-tt1210166]",
		},
		{
			name: "colon in title",
			item: MediaItem{Name: "Sword Art Online: Extra Edition (2013) [imdb-tt3241196]", IsFolder: true},
			want: "Sword Art Online： Extra Edition (2013) [imdb-tt3241196]",
		},
		{
			name: "clean title unchanged",
			item: MediaItem{Name: "Le Voyage de Chihiro (2001) [imdb-tt0245429]", IsFolder: true},
			want: "Le Voyage de Chihiro (2001) [imdb-tt0245429]",
		},
		{
			name: "file with extension",
			item: MediaItem{Name: "Superman/Batman: Apocalypse (2010) [imdb-tt1210166]", Extension: ".mkv"},
			want: "Superman∕Batman： Apocalypse (2010) [imdb-tt1210166].mkv",
		},
		{
			name: "all illegal windows chars",
			item: MediaItem{Name: `a/b\c:d*e?f"g<h>i|j`, IsFolder: true},
			want: "a∕b＼c：d＊e？f＂g＜h＞i｜j",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.item.VFSName()
			if got != tc.want {
				t.Errorf("VFSName() = %q, want %q", got, tc.want)
			}
		})
	}
}
