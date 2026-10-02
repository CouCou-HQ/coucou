#!/usr/bin/env bash
# Runs scripts/sound against generated tones: names, tags, folders, skips, and real Ogg Opus out.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
t=$(mktemp -d)
trap 'rm -rf "$t"' EXIT
tone() { ffmpeg -nostdin -loglevel error -f lavfi -i "sine=frequency=440:duration=0.3" "$@"; }

mkdir -p "$t/in/nested"
tone "$t/in/Wet Fart 3.mp3"
tone "$t/in/nested/Big-Burp!.wav"
tone "$t/in/Perfect Fart.rare.m4a"
tone "$t/in/Ufufu.NSFW.Rare.ogg"
tone "$t/Moan.nsfw.wav"
tone "$t/Loud One.flac"
# An mp3 with cover art: the case -vn exists for.
ffmpeg -nostdin -loglevel error -f lavfi -i "sine=duration=0.3" -f lavfi -i "color=red:s=32x32:d=1" \
	-map 0 -map 1 -c:v mjpeg -frames:v 1 -disposition:v attached_pic "$t/in/Cover Art.mp3"

"$here/sound" -o "$t/out" "$t/in" >/dev/null
"$here/sound" -r -o "$t/out" "$t/Loud One.flac" >/dev/null
"$here/sound" --rare -o "$t/out" "$t/Moan.nsfw.wav" >/dev/null

want="big_burp.ogg cover_art.ogg loud_one.rare.ogg moan.rare.nsfw.ogg perfect_fart.rare.ogg ufufu.rare.nsfw.ogg wet_fart_3.ogg"
got=$(cd "$t/out" && ls | tr '\n' ' ' | sed 's/ $//')
[[ $got == "$want" ]] || { echo "FAIL names: got '$got', want '$want'"; exit 1; }

for f in "$t"/out/*.ogg; do
	codec=$(ffprobe -v error -show_entries stream=codec_type,codec_name -of csv=p=0 "$f" | tr '\n' ' ')
	[[ $codec == "opus,audio " ]] || { echo "FAIL $f: streams '$codec', want only opus audio"; exit 1; }
done

out=$("$here/sound" -o "$t/out" "$t/in/Wet Fart 3.mp3")
[[ $out == *"skip"*"exists"* ]] || { echo "FAIL: an existing output was not skipped"; exit 1; }

dry=$("$here/sound" -n -r -o "$t/elsewhere" "$t/in/Wet Fart 3.mp3")
[[ $dry == *"elsewhere/wet_fart_3.rare.ogg"* && ! -e $t/elsewhere ]] || { echo "FAIL: dry run wrote or misnamed"; exit 1; }

dry=$("$here/sound" -n -x -o "$t/elsewhere" "$t/in/Wet Fart 3.mp3")
[[ $dry == *"elsewhere/wet_fart_3.nsfw.ogg"* ]] || { echo "FAIL: -x did not tag nsfw: $dry"; exit 1; }

echo "ok"
