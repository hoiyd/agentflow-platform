# Product Recordings

Recorded on 2026-10-08 using the production Go API and Next.js frontend, a
disposable PostgreSQL database, live DeepSeek model responses, and local Ollama
embeddings. Tasks and source documents contain public sample data only. These
are edited walkthroughs, not latency benchmarks: waiting periods are shortened
and reading pauses added. No UI state or model output is fabricated.

## Scenarios

| Asset | What it demonstrates | Recorded Run |
| --- | --- | --- |
| `multi-agent.gif` | Human plan approval, Calculator rounds, all five stages completed, Replay and per-attempt usage. A Calculator error remains visible in the Run's evidence. | `run_09bc3fb7772f45ea` |
| `knowledge.gif` | Upload [example.md](../../../../examples/example.md), index with Ollama, hybrid retrieval, source-backed answer, and Context Manifest. | `run_ff42a6e9f6c8d8f6` |
| `verification.gif` | Required `Owner` and `Rollback` phrases reject a draft title; a separate corrected task passes the same completion contract. Both original outcomes remain persisted. | Failed: `run_835e63a071d24767`; passed: `run_1e5a4bf181002849` |

The isolated recording Run IDs are provenance, not links to Runs in the user's
database. The verification example proves deterministic text constraints, not
factual correctness or actual release approval. The Multi-Agent example does
not execute a deployment.

## Quality and Playback

- Browser viewport: 1440 x 900 CSS pixels at device scale factor 2.
- Native capture and GIF size: **2880 x 1800**, without upscaling or a lossy video
  intermediate. PNG frames are encoded at 4 fps using a 256-color palette and
  no dithering to preserve text edges. GIF's palette remains a format limit.
- Each matching `.webp` is a lossless copy of the recording's first frame.
- The landing display remains bounded to 960 CSS pixels, preserves the entire
  frame, mounts only the selected recording, and lazy-loads it.
- Pause switches to the static first frame; Play restarts the animation. Native
  `<picture>` selects the static image for reduced motion before hydration.
- Image errors link to the matching GIF in this directory on GitHub.

The older `docs/assets/` GIFs and root README gallery are unchanged.

## Updating a Recording

Capture real UI interactions against an isolated database using public tasks.
Inspect the resulting Replay and terminal statuses before publishing. Preserve
failure evidence and distinguish a new corrected Run from a resumed Run.
Convert native PNG frames directly, then extract the first frame as the poster:

```bash
ffmpeg -framerate 4 -i '<frames>/%04d.png' \
  -filter_complex '[0:v]split[a][b];[a]palettegen=max_colors=256:stats_mode=diff[p];[b][p]paletteuse=dither=none:diff_mode=rectangle' \
  -loop 0 <destination.gif>
ffmpeg -i '<frames>/0000.png' -frames:v 1 \
  -c:v libwebp -lossless 1 <destination.webp>
```

FFmpeg is needed only to refresh assets, not to build or run the application.
