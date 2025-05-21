package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// Bit shifting is a way to multiply by powers of 2. 10 << 20 is the same as 10 * 1024 * 1024, which is 10MB.
	const MaxMemory = 10 << 20
	r.ParseMultipartForm(MaxMemory)

	multipartFile, header, err := r.FormFile("thumbnail")
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
	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Invalid file type", nil)
		return
	}

	assetPath := getAssetPath(mediaType)
	assetDiskPath := cfg.getAssetDiskPath(assetPath)

	// Use os.Create to create the new file
	destinationFile, err := os.Create(assetDiskPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error in creating filepath", err)
		return
	}
	defer destinationFile.Close()
	//Copy the contents from the multipart.File to the new file on disk using io.Copy
	if _, err = io.Copy(destinationFile, multipartFile); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error in copying to destination filepath", err)
		return
	}

	// Get the video's metadata from the SQLite database. The apiConfig's db has a GetVideo method you can use
	videoMetadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not retrieve video metadata with provided ID", err)
		return
	}
	//If the authenticated user is not the video owner, return a http.StatusUnauthorized response
	if videoMetadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User ID does not match the video creator's ID", nil)
		return
	}

	// Thumbnail URL: http://localhost:<port>/assets/<videoID>.<file_extension>
	url := cfg.getAssetURL(assetPath)
	videoMetadata.ThumbnailURL = &url
	err = cfg.db.UpdateVideo(videoMetadata)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoMetadata)
}
