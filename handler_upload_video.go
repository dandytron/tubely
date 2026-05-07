package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {

	// Bit shifting is a way to multiply by powers of 2. 10 << 20 is the same as 10 * 1024 * 1024, which is 10MB.
	const UploadLimit int64 = 1 << 30

	// Set the request body to something that limits uploads to 1 GB.
	maxBytesReader := http.MaxBytesReader(w, r.Body, UploadLimit)
	r.Body = maxBytesReader

	// Extract the videoID from the URL path parameters and parse it as a UUID
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	// Authenticate the user to get a userID
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading video: ", videoID, " by user: ", userID)

	// Get the video's metadata from the SQLite database. The apiConfig's db has a GetVideo method you can use
	videoMetadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not retrieve video metadata with provided ID", err)
		return
	}
	// If the authenticated user is not the video owner, return a http.StatusUnauthorized response
	if videoMetadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User ID does not match the video creator's ID", nil)
		return
	}

	// Parse the uploaded video file from the form data
	multipartFile, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer multipartFile.Close()

	// Get the media type from the form file's Content-Type header
	// Use the mime.ParseMediaType function to get the media type from the Content-Type header
	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not parse header content-type into mediatype", err)
		return
	}

	// Instead of encoding to base64, update the handler to save the bytes to a file at the path /assets/<videoID>.<file_extension>
	// Use the Content-Type header to determine the file extension.
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid video file type", nil)
		return
	}

	// Save the uploaded file to a temporary file on disk.
	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not create temp video file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// io.Copy the contents over from the wire to the temp file
	if _, err = io.Copy(tempFile, multipartFile); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not copy video file", err)
		return
	}

	// Reset the tempFile's file pointer to the beginning
	if _, err = tempFile.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not seek to beginning of temp file", err)
		return
	}

	aspectRatio, err := cfg.getAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not get aspect ratio of video", err)
		return
	}

	var prefix string
	switch aspectRatio {
	case "16:9":
		prefix = "landscape"
	case "9:16":
		prefix = "portrait"
	default:
		prefix = "other"
	}

	key := fmt.Sprintf("%s/%s.mp4", prefix, videoID.String())

	// Process the video for fast start
	processedFilePath, err := cfg.processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not process video for fast start", err)
		return
	}
	defer os.Remove(processedFilePath)

	// Open the processed file for reading
	processedFile, err := os.Open(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not open processed video file", err)
		return
	}
	defer processedFile.Close()

	// Put the object into S3 using PutObject
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(key),
		Body:        processedFile,
		ContentType: aws.String(mediaType),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error uploading file to S3", err)
		return
	}

	// Thumbnail URL: http://localhost:<port>/assets/<videoID>.<file_extension>
	s3Url := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, key)
	videoMetadata.VideoURL = &s3Url
	err = cfg.db.UpdateVideo(videoMetadata)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not update video metadata", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoMetadata)
}

type ffprobeStream struct {
	Width              int    `json:"width"`
	Height             int    `json:"height"`
	DisplayAspectRatio string `json:"display_aspect_ratio"`
}

type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
}

func (cfg *apiConfig) getAspectRatio(filepath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filepath)

	var buffer bytes.Buffer
	cmd.Stdout = &buffer

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("error running ffprobe command: %w", err)
	}

	// buffer.Bytes() or buffer.String() now holds ffprobe's output
	// You can parse this output to extract the aspect ratio information you need

	var result ffprobeOutput
	if err := json.Unmarshal(buffer.Bytes(), &result); err != nil {
		return "", fmt.Errorf("error parsing ffprobe output: %w", err)
	}

	switch result.Streams[0].DisplayAspectRatio {
	case "16:9":
		return "16:9", nil
	case "9:16":
		return "9:16", nil
	default:
		return "other", nil
	}
}

// This function takes in a file path to a video, and uses ffmpeg to process the video for fast start.
// It returns the file path to the processed video.
// The output file path can be the input file path with ".processing" appended to it.
// The ffmpeg command should be: ffmpeg -i <input_file_path> -c copy -movflags faststart -f mp4 <output_file_path>

func (cfg *apiConfig) processVideoForFastStart(filepath string) (string, error) {
	output_filepath := filepath + ".processing"

	// the arguments are -i, the input file path, -c, copy, -movflags, faststart, -f, mp4 and the output file path.

	cmd := exec.Command("ffmpeg", "-i", filepath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", output_filepath)

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("error running ffmpeg command: %w", err)
	}

	return output_filepath, nil
}
