// Package nfo generates Kodi-compatible NFO XML files for media items.
package nfo

import (
	"encoding/xml"
	"fmt"

	"github.com/CCoupel/Media_FS/internal/connector"
)

// Movie generates a movie.nfo XML payload.
func Movie(meta connector.ItemMetadata) ([]byte, error) {
	type uniqueID struct {
		Type    string `xml:"type,attr"`
		Default bool   `xml:"default,attr,omitempty"`
		Value   string `xml:",chardata"`
	}
	type movie struct {
		XMLName   xml.Name   `xml:"movie"`
		Title     string     `xml:"title"`
		Year      int        `xml:"year,omitempty"`
		Plot      string     `xml:"plot,omitempty"`
		Rating    float64    `xml:"rating,omitempty"`
		Genres    []string   `xml:"genre"`
		Directors []string   `xml:"director"`
		Runtime   int        `xml:"runtime,omitempty"` // minutes
		UniqueIDs []uniqueID `xml:"uniqueid"`
	}

	m := movie{
		Title:     meta.Name,
		Year:      meta.Year,
		Plot:      meta.Overview,
		Rating:    meta.Rating,
		Genres:    meta.Genres,
		Directors: meta.Directors,
		Runtime:   int(meta.RunTimeTicks / 600_000_000), // ticks → minutes
	}
	for k, v := range meta.ExternalIDs {
		m.UniqueIDs = append(m.UniqueIDs, uniqueID{Type: k, Value: v})
	}

	out, err := xml.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}

// TVShow generates a tvshow.nfo XML payload.
func TVShow(meta connector.ItemMetadata) ([]byte, error) {
	type uniqueID struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	}
	type tvshow struct {
		XMLName   xml.Name   `xml:"tvshow"`
		Title     string     `xml:"title"`
		Year      int        `xml:"year,omitempty"`
		Plot      string     `xml:"plot,omitempty"`
		Rating    float64    `xml:"rating,omitempty"`
		Genres    []string   `xml:"genre"`
		UniqueIDs []uniqueID `xml:"uniqueid"`
	}

	s := tvshow{
		Title:  meta.Name,
		Year:   meta.Year,
		Plot:   meta.Overview,
		Rating: meta.Rating,
		Genres: meta.Genres,
	}
	for k, v := range meta.ExternalIDs {
		s.UniqueIDs = append(s.UniqueIDs, uniqueID{Type: k, Value: v})
	}

	out, err := xml.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}

// Episode generates an episode.nfo XML payload.
func Episode(meta connector.ItemMetadata, season, episode int) ([]byte, error) {
	type ep struct {
		XMLName xml.Name `xml:"episodedetails"`
		Title   string   `xml:"title"`
		Season  int      `xml:"season"`
		Episode int      `xml:"episode"`
		Plot    string   `xml:"plot,omitempty"`
		Rating  float64  `xml:"rating,omitempty"`
		Runtime int      `xml:"runtime,omitempty"`
	}

	e := ep{
		Title:   meta.Name,
		Season:  season,
		Episode: episode,
		Plot:    meta.Overview,
		Rating:  meta.Rating,
		Runtime: int(meta.RunTimeTicks / 600_000_000),
	}

	out, err := xml.MarshalIndent(e, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}

// Filename returns the expected .nfo filename for a given media filename.
// e.g. "Inception.mkv" → "Inception.nfo"
func Filename(mediaFilename string) string {
	for i := len(mediaFilename) - 1; i >= 0; i-- {
		if mediaFilename[i] == '.' {
			return mediaFilename[:i] + ".nfo"
		}
	}
	return fmt.Sprintf("%s.nfo", mediaFilename)
}
