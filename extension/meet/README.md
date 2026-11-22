# Sonara · Google Meet

Prototype: tells Sonara who is speaking in Google Meet, so "Participante 2"
becomes "Participante 2 (Walter)".

1. Open `chrome://extensions`, turn on *Developer mode*, click *Load unpacked* and pick this folder.
2. Open Sonara and start recording the meeting.
3. In Meet, turn on **captions** (CC button). The extension reads the speaker name from each caption.

Sonara listens on `127.0.0.1:47823` and only accepts requests from extensions.
Names are assigned by matching the moments Meet showed each person speaking
against the segments of each separated voice.
