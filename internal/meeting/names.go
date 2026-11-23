package meeting

import (
	"fmt"
	"strings"

	"sonara/internal/storage"
)

// GenericSpeakerToken stands in for an empty speaker_key (the undiarized,
// single "Participante" bucket) in the text sent to the model: an LLM given
// a literal blank label as a JSON key is a bad bet, this isn't.
const GenericSpeakerToken = "generico"

// MicSpeakerTokenPrefix marks a participant separated within the microphone,
// so its token never collides with a meeting_output key.
const MicSpeakerTokenPrefix = "mic_"

// NameInferenceTranscript lists what was actually said, one line per
// spoken segment, labeled by speaker token. The microphone is always "Yo" —
// context for the model, never something it gets asked to rename.
func NameInferenceTranscript(segments []storage.TranscriptSegment) string {
	var out strings.Builder
	for _, segment := range segments {
		if segment.Status != storage.SegmentOK || strings.TrimSpace(segment.Text) == "" {
			continue
		}
		token := "Yo"
		if segment.Track == storage.TrackMicrophone && segment.SpeakerKey != "" {
			token = MicSpeakerTokenPrefix + segment.SpeakerKey
		}
		if segment.Track == storage.TrackOutput {
			token = segment.SpeakerKey
			if token == "" {
				token = GenericSpeakerToken
			}
		}
		fmt.Fprintf(&out, "%s: %s\n", token, segment.Text)
	}
	return out.String()
}
