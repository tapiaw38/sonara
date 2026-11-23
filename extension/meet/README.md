# Sonara · Google Meet

Prototype: tells Sonara who is speaking in Google Meet, so "Participante 2"
becomes "Participante 2 (Walter)".

1. Open `chrome://extensions`, turn on *Developer mode*, click *Load unpacked* and pick this folder.
2. Open Sonara and start recording the meeting.
3. In Meet, turn on **captions** (CC button). The extension reads the speaker name from each caption.

Sonara listens on `127.0.0.1:47823` and only accepts requests from extensions.
Names are assigned by matching the moments Meet showed each person speaking
against the segments of each separated voice.

By default, Sonara accepts any `chrome-extension://` origin because an unpacked
extension's ID depends on its install path. To pin the origin, copy the Sonara
extension ID from `chrome://extensions` and set `SONARA_MEET_EXTENSION_ID` to it
before starting Sonara.

Sonara stores up to 50 distinct names and 5,000 speaking spans per meeting.
It acknowledges discarded events with HTTP 204.
