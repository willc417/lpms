package ffmpeg

import (
	"fmt"
	"os"
	"testing"
)

func TestAPI_SkippedSegment(t *testing.T) {
	// Ensure that things are still OK even if there are gaps in between segments
	// We should see a perf slowdown if the filters are in the wrong order
	// (fps before scale).
	// Would be nice to measure this somehow, eg counting number of flushed frames.

	// Really should refactor this test to increase commonality with other
	// tests that also check things like SSIM, MD5 hashes, etc...
	// See TestNvidia_API_MixedOutput / TestTranscoder_EncoderOpts / TestTranscoder_StreamCopy
	run, dir := setupTest(t)
	err := RTMPToHLS("../transcoder/test.ts", dir+"/out.m3u8", dir+"/out_%d.ts", "2", 0)
	if err != nil {
		t.Error(err)
	}

	profile := P144p30fps16x9
	profile.Framerate = 123
	idx := []int{0, 3}
	tc := NewTranscoder()
	defer tc.StopTranscoder()
	for _, i := range idx {
		in := &TranscodeOptionsIn{Fname: fmt.Sprintf("%s/out_%d.ts", dir, i)}
		out := []TranscodeOptions{{
			Oname:        fmt.Sprintf("%s/%d.md5", dir, i),
			AudioEncoder: ComponentOptions{Name: "drop"},
			VideoEncoder: ComponentOptions{Name: "copy"},
			Muxer:        ComponentOptions{Name: "md5"},
		}, {
			Oname:        fmt.Sprintf("%s/sw_%d.ts", dir, i),
			Profile:      profile,
			AudioEncoder: ComponentOptions{Name: "copy"},
		}}
		res, err := tc.Transcode(in, out)
		if err != nil {
			t.Error(err)
		}
		if res.Decoded.Frames != 120 {
			t.Error("Did not get decoded frames", res.Decoded.Frames)
		}
		if res.Encoded[1].Frames != 246 {
			t.Error("Did not get encoded frames ", res.Encoded[1].Frames)
		}
	}
	cmd := `
    function check {

      # Check md5sum for stream copy / drop
      ffmpeg -loglevel warning -i out_$1.ts -an -c:v copy -f md5 ffmpeg_$1.md5
      diff -u $1.md5 ffmpeg_$1.md5

      # muxdelay removes the 1.4 sec mpegts offset, copyts passes ts through
      ffmpeg -loglevel warning -i out_$1.ts -c:a aac -ar 44100 -ac 2 \
        -vf fps=123,scale=w=256:h=144 -c:v libx264 -muxdelay 0 -copyts ffmpeg_sw_$1.ts

      # sanity check ffmpeg frame count against ours
      ffprobe -count_frames -show_streams -select_streams v ffmpeg_sw_$1.ts | grep nb_read_frames=246
      ffprobe -count_frames -show_streams -select_streams v sw_$1.ts | grep nb_read_frames=246

   # check image quality
    ffmpeg -loglevel warning -i sw_$1.ts -i ffmpeg_sw_$1.ts \
      -lavfi "[0:v][1:v]ssim=sw_stats_$1.log" -f null -
    grep -Po 'All:\K\d+.\d+' sw_stats_$1.log | \
      awk '{ if ($1 < 0.95) count=count+1 } END{ exit count > 5 }'

    # Really should check relevant audio as well...
    }


    check 0
    check 3
  `
	run(cmd)

}

func TestTranscoderAPI_InvalidFile(t *testing.T) {
	// Test the following file open results on input: fail, success, fail, success

	tc := NewTranscoder()
	defer tc.StopTranscoder()
	in := &TranscodeOptionsIn{}
	out := []TranscodeOptions{{
		Oname:        "-",
		AudioEncoder: ComponentOptions{Name: "copy"},
		VideoEncoder: ComponentOptions{Name: "drop"},
		Muxer:        ComponentOptions{Name: "null"},
	}}

	// fail # 1
	in.Fname = "none"
	_, err := tc.Transcode(in, out)
	if err == nil || err.Error() != "No such file or directory" {
		t.Error("Expected 'No such file or directory', got ", err)
	}

	// success # 1
	in.Fname = "../transcoder/test.ts"
	_, err = tc.Transcode(in, out)
	if err != nil {
		t.Error(err)
	}

	// fail # 2
	in.Fname = "none"
	_, err = tc.Transcode(in, out)
	if err == nil || err.Error() != "No such file or directory" {
		t.Error("Expected 'No such file or directory', got ", err)
	}

	// success # 2
	in.Fname = "../transcoder/test.ts"
	_, err = tc.Transcode(in, out)
	if err != nil {
		t.Error(err)
	}

	// Now check invalid output filename
	out[0].Muxer = ComponentOptions{Name: "md5"}
	out[0].Oname = "/not/really/anywhere"
	_, err = tc.Transcode(in, out)
	if err == nil {
		t.Error(err)
	}

}

func TestTranscoderAPI_Stopped(t *testing.T) {

	// Test stopped transcoder
	tc := NewTranscoder()
	tc.StopTranscoder()
	in := &TranscodeOptionsIn{}
	_, err := tc.Transcode(in, nil)
	if err != ErrTranscoderStp {
		t.Errorf("Unexpected error; wanted %v but got %v", ErrTranscoderStp, err)
	}

	// test somehow munged transcoder handle
	tc2 := NewTranscoder()
	tc2.handle = nil // technically this leaks memory ... OK for test
	_, err = tc2.Transcode(in, nil)
	if err != ErrTranscoderStp {
		t.Errorf("Unexpected error; wanted %v but got %v", ErrTranscoderStp, err)
	}
}

func TestTranscoderAPI_TooManyOutputs(t *testing.T) {

	out := make([]TranscodeOptions, 11)
	for i := range out {
		out[i].VideoEncoder = ComponentOptions{Name: "drop"}
	}
	in := &TranscodeOptionsIn{}
	tc := NewTranscoder()
	_, err := tc.Transcode(in, out)
	if err == nil || err.Error() != "Too many outputs" {
		t.Error("Expected 'Too many outputs', got ", err)
	}
}

func countEncodedFrames(t *testing.T, accel Acceleration) {
	run, dir := setupTest(t)
	defer os.RemoveAll(dir)

	cmd := `
    # run segmenter and sanity check frame counts . Hardcode for now.
    ffmpeg -loglevel warning -i "$1"/../transcoder/test.ts -c:a copy -c:v copy -f hls test.m3u8
    ffprobe -loglevel warning -select_streams v -count_frames -show_streams test0.ts | grep nb_read_frames=120
    ffprobe -loglevel warning -select_streams v -count_frames -show_streams test1.ts | grep nb_read_frames=120
    ffprobe -loglevel warning -select_streams v -count_frames -show_streams test2.ts | grep nb_read_frames=120
    ffprobe -loglevel warning -select_streams v -count_frames -show_streams test3.ts | grep nb_read_frames=120
  `
	run(cmd)

	tc := NewTranscoder()
	defer tc.StopTranscoder()

	// Test encoding
	for i := 0; i < 4; i++ {
		in := &TranscodeOptionsIn{
			Fname:  fmt.Sprintf("%s/test%d.ts", dir, i),
			Accel:  accel,
			Device: "0",
		}
		p60fps := P144p30fps16x9
		p60fps.Framerate = 60
		p120fps := P144p30fps16x9
		p120fps.Framerate = 120
		passthru := P144p30fps16x9
		passthru.Framerate = 0
		out := []TranscodeOptions{{
			Oname:   fmt.Sprintf("%s/out_30fps_%d.ts", dir, i),
			Profile: P144p30fps16x9,
			Accel:   accel,
		}, {
			Oname:   fmt.Sprintf("%s/out_60fps_%d.ts", dir, i),
			Profile: p60fps,
			Accel:   accel,
		}, {
			Oname:   fmt.Sprintf("%s/out_120fps_%d.ts", dir, i),
			Profile: p120fps,
			Accel:   accel,
		}, {
			Oname:   fmt.Sprintf("%s/out_passthru_%d.ts", dir, i),
			Profile: passthru,
			Accel:   accel,
		}}

		res, err := tc.Transcode(in, out)
		if err != nil {
			t.Error(err)
		}
		if res.Encoded[0].Frames != 60 {
			t.Error(in.Fname, " Mismatched frame count: expected 60 got ", res.Encoded[0].Frames)
		}
		if res.Encoded[1].Frames != 120 {
			t.Error(in.Fname, " Mismatched frame count: expected 120 got ", res.Encoded[1].Frames)
		}
		if res.Encoded[2].Frames != 240 {
			t.Error(in.Fname, " Mismatched frame count: expected 240 got ", res.Encoded[2].Frames)
		}
		if res.Encoded[3].Frames != 120 {
			t.Error(in.Fname, " Mismatched frame count: expected 120 got ", res.Encoded[3].Frames)
		}
	}

	// Check timestamps of the results we just got.
	// A bit brute force but another layer of checking
	// First output the expected PTS - first and last 3 frames, per segment
	//  (hardcoded below)
	// Second calculate the same set of PTS for transcoded results
	// Then take the diff of the two. Should match.

	// Write expected PTS to a file for diff'ing
	//  (Calculated by running the same routine below)
	cmd = `
  cat << EOF > expected_pts.out
==> out_120fps_0.ts.pts <==
pkt_pts=129000
pkt_pts=129750
pkt_pts=130500
pkt_pts=306750
pkt_pts=307500
pkt_pts=308250

==> out_120fps_1.ts.pts <==
pkt_pts=309000
pkt_pts=309750
pkt_pts=310500
pkt_pts=486750
pkt_pts=487500
pkt_pts=488250

==> out_120fps_2.ts.pts <==
pkt_pts=489000
pkt_pts=489750
pkt_pts=490500
pkt_pts=666750
pkt_pts=667500
pkt_pts=668250

==> out_120fps_3.ts.pts <==
pkt_pts=669000
pkt_pts=669750
pkt_pts=670500
pkt_pts=846750
pkt_pts=847500
pkt_pts=848250

==> out_30fps_0.ts.pts <==
pkt_pts=129000
pkt_pts=132000
pkt_pts=135000
pkt_pts=300000
pkt_pts=303000
pkt_pts=306000

==> out_30fps_1.ts.pts <==
pkt_pts=309000
pkt_pts=312000
pkt_pts=315000
pkt_pts=480000
pkt_pts=483000
pkt_pts=486000

==> out_30fps_2.ts.pts <==
pkt_pts=489000
pkt_pts=492000
pkt_pts=495000
pkt_pts=660000
pkt_pts=663000
pkt_pts=666000

==> out_30fps_3.ts.pts <==
pkt_pts=669000
pkt_pts=672000
pkt_pts=675000
pkt_pts=840000
pkt_pts=843000
pkt_pts=846000

==> out_60fps_0.ts.pts <==
pkt_pts=129000
pkt_pts=130500
pkt_pts=132000
pkt_pts=304500
pkt_pts=306000
pkt_pts=307500

==> out_60fps_1.ts.pts <==
pkt_pts=309000
pkt_pts=310500
pkt_pts=312000
pkt_pts=484500
pkt_pts=486000
pkt_pts=487500

==> out_60fps_2.ts.pts <==
pkt_pts=489000
pkt_pts=490500
pkt_pts=492000
pkt_pts=664500
pkt_pts=666000
pkt_pts=667500

==> out_60fps_3.ts.pts <==
pkt_pts=669000
pkt_pts=670500
pkt_pts=672000
pkt_pts=844500
pkt_pts=846000
pkt_pts=847500

==> out_passthru_0.ts.pts <==
pkt_pts=128970
pkt_pts=130500
pkt_pts=131940
pkt_pts=304470
pkt_pts=305910
pkt_pts=307440

==> out_passthru_1.ts.pts <==
pkt_pts=308970
pkt_pts=310500
pkt_pts=311940
pkt_pts=484470
pkt_pts=485910
pkt_pts=487440

==> out_passthru_2.ts.pts <==
pkt_pts=488970
pkt_pts=490410
pkt_pts=491940
pkt_pts=664470
pkt_pts=665910
pkt_pts=667440

==> out_passthru_3.ts.pts <==
pkt_pts=668970
pkt_pts=670500
pkt_pts=671940
pkt_pts=844470
pkt_pts=845910
pkt_pts=847440
EOF
  `
	run(cmd)

	// Calculate first and last 3 frame PTS for each output, and compare
	cmd = `
    # First and last 3 frame PTS for each output
    FILES=out_*.ts

    for f in  $FILES
    do
      ffprobe -loglevel warning -select_streams v -show_frames $f | grep pkt_pts= | head -3 > $f.pts
      ffprobe -loglevel warning -select_streams v -show_frames $f | grep pkt_pts= | tail -3 >> $f.pts
    done
    tail -n +1 out_*.ts.pts > transcoded_pts.out

    # Do the comparison!
    diff -u expected_pts.out transcoded_pts.out
  `
	run(cmd)

}

func TestTranscoderAPI_CountEncodedFrames(t *testing.T) {
	countEncodedFrames(t, Software)
}

func TestTranscoder_API_AlternatingTimestamps(t *testing.T) {
	// Really should refactor this test to increase commonality with other
	// tests that also check things like SSIM, MD5 hashes, etc...
	// See TestNvidia_API_MixedOutput / TestTranscoder_EncoderOpts / TestTranscoder_StreamCopy / TestNvidia_API_AlternatingTimestamps
	run, dir := setupTest(t)
	err := RTMPToHLS("../transcoder/test.ts", dir+"/out.m3u8", dir+"/out_%d.ts", "2", 0)
	if err != nil {
		t.Error(err)
	}

	profile := P144p30fps16x9
	profile.Framerate = 123
	tc := NewTranscoder()
	defer tc.StopTranscoder()
	idx := []int{1, 0, 3, 2}
	for _, i := range idx {
		in := &TranscodeOptionsIn{Fname: fmt.Sprintf("%s/out_%d.ts", dir, i)}
		out := []TranscodeOptions{{
			Oname:        fmt.Sprintf("%s/%d.md5", dir, i),
			AudioEncoder: ComponentOptions{Name: "drop"},
			VideoEncoder: ComponentOptions{Name: "copy"},
			Muxer:        ComponentOptions{Name: "md5"},
		}, {
			Oname:        fmt.Sprintf("%s/sw_%d.ts", dir, i),
			Profile:      profile,
			AudioEncoder: ComponentOptions{Name: "copy"},
		}, {
			Oname:   fmt.Sprintf("%s/sw_audio_encode_%d.ts", dir, i),
			Profile: profile,
		}}
		res, err := tc.Transcode(in, out)
		if (i == 1 || i == 3) && err != nil {
			t.Error(err)
		}
		if i == 0 || i == 2 {
			if err == nil || err.Error() != "Segment out of order" {
				t.Error(err)
			}
			// Maybe one day we'll be able to run the rest of this test
			continue
		}
		if res.Decoded.Frames != 120 {
			t.Error("Did not get decoded frames", res.Decoded.Frames)
		}
		if res.Encoded[1].Frames != res.Encoded[2].Frames {
			t.Error("Mismatched frame count for hw/nv")
		}
	}
	cmd := `
    function check {

      # Check md5sum for stream copy / drop
      ffmpeg -loglevel warning -i out_$1.ts -an -c:v copy -f md5 ffmpeg_$1.md5
      diff -u $1.md5 ffmpeg_$1.md5

      ffmpeg -loglevel warning -i out_$1.ts -c:a aac -ar 44100 -ac 2 \
        -vf fps=123,scale=w=256:h=144 -c:v libx264 ffmpeg_sw_$1.ts

      # sanity check ffmpeg frame count against ours
      ffprobe -count_frames -show_streams -select_streams v ffmpeg_sw_$1.ts | grep nb_read_frames=246
      ffprobe -count_frames -show_streams -select_streams v sw_$1.ts | grep nb_read_frames=246
      ffprobe -count_frames -show_streams -select_streams v sw_audio_encode_$1.ts | grep nb_read_frames=246

    # check image quality
    ffmpeg -loglevel warning -i sw_$1.ts -i ffmpeg_sw_$1.ts \
      -lavfi "[0:v][1:v]ssim=sw_stats_$1.log" -f null -
    grep -Po 'All:\K\d+.\d+' sw_stats_$1.log | \
      awk '{ if ($1 < 0.95) count=count+1 } END{ exit count > 5 }'

    # Really should check relevant audio as well...
    }


    # re-enable for seg 0 and 1 when alternating timestamps can be handled
    # check 0
    check 1
    # check 2
    check 3
  `
	run(cmd)
}
