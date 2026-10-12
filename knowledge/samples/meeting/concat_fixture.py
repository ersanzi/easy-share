"""把 make_fixture.ps1 产出的分句 WAV 拼成一段会议测试音频（stdlib wave，格式须一致）。

用法: python concat_fixture.py <分段wav...> --out meeting_test.wav
段间默认静音 600ms，模拟真实会议的换人停顿。
"""
from __future__ import annotations

import argparse
import wave
from pathlib import Path


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("segments", nargs="+", help="按发言顺序排列的分段 WAV")
    parser.add_argument("--out", required=True)
    parser.add_argument("--gap-ms", type=int, default=600)
    args = parser.parse_args()

    params: tuple[int, int, int] | None = None
    silence = b""
    with wave.open(args.out, "wb") as out:
        for segment in args.segments:
            with wave.open(segment, "rb") as seg:
                current = (seg.getnchannels(), seg.getsampwidth(), seg.getframerate())
                if params is None:
                    params = current
                    out.setnchannels(current[0])
                    out.setsampwidth(current[1])
                    out.setframerate(current[2])
                    silence = b"\x00" * (current[0] * current[1]) * int(current[2] * args.gap_ms / 1000)
                if current != params:
                    raise SystemExit(f"格式不一致: {segment} {current} != {params}")
                out.writeframes(seg.readframes(seg.getnframes()))
                out.writeframes(silence)
    duration = Path(args.out).stat().st_size
    print(f"wrote {args.out} ({duration} bytes)")


if __name__ == "__main__":
    main()
