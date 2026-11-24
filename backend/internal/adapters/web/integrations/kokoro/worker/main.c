#define _POSIX_C_SOURCE 200809L
#include <sherpa-onnx/c-api/c-api.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

static void reply(const char *text) {
  puts(text);
  fflush(stdout);
}

int main(int argc, char **argv) {
  // stdin: "speaker speed byte-count\n" followed by exactly byte-count bytes.
  // stdout: ready, then ok/error for each request; audio goes to a private WAV.
  if (argc != 5 || chdir(argv[1]) != 0) return 1;
  SherpaOnnxOfflineTtsConfig config = {0};
  config.model.kokoro.model = "model.int8.onnx";
  config.model.kokoro.voices = "voices.bin";
  config.model.kokoro.tokens = "tokens.txt";
  config.model.kokoro.data_dir = "espeak-ng-data";
  config.model.kokoro.lang = argv[2];
  config.model.kokoro.length_scale = 1;
  config.model.num_threads = 2;
  config.model.provider = argv[3];
  config.max_num_sentences = 1;
  const SherpaOnnxOfflineTts *tts = SherpaOnnxCreateOfflineTts(&config);
  if (!tts) return 2;
  reply("ready");
  char header[128];
  while (fgets(header, sizeof(header), stdin)) {
    SherpaOnnxGenerationConfig generation = {0};
    size_t size;
    int sid;
    if (sscanf(header, "%d %f %zu", &sid, &generation.speed, &size) != 3 ||
        size == 0 || size > 65536 || generation.speed < 0.5f ||
        generation.speed > 1.5f) break;
    generation.sid = sid;
    generation.silence_scale = 0.2f;
    char *text = malloc(size + 1);
    if (!text) break;
    if (fread(text, 1, size, stdin) != size) {
      free(text);
      break;
    }
    text[size] = 0;
    const SherpaOnnxGeneratedAudio *audio =
        SherpaOnnxOfflineTtsGenerateWithConfig(tts, text, &generation, NULL, NULL);
    free(text);
    if (!audio) {
      reply("error");
      continue;
    }
    int ok = audio->n > 0 && SherpaOnnxWriteWave(
        audio->samples, audio->n, audio->sample_rate, argv[4]);
    SherpaOnnxDestroyOfflineTtsGeneratedAudio(audio);
    reply(ok ? "ok" : "error");
  }
  SherpaOnnxDestroyOfflineTts(tts);
  return 0;
}
