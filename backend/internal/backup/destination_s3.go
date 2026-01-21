package backup

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"path"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

// S3Destination stores backups in AWS S3 or S3-compatible storage
type S3Destination struct {
	config  *DestinationConfig
	s3Client *s3.S3
}

// NewS3Destination creates a new S3 destination
func NewS3Destination(config *DestinationConfig) (*S3Destination, error) {
	// Build AWS config
	awsConfig := &aws.Config{
		Region: aws.String(config.S3Region),
		Credentials: credentials.NewStaticCredentials(
			config.S3AccessKey,
			config.S3SecretKey,
			"",
		),
	}

	// Custom endpoint for S3-compatible storage (MinIO, DigitalOcean Spaces, etc.)
	if config.S3Endpoint != "" {
		awsConfig.Endpoint = aws.String(config.S3Endpoint)
		awsConfig.S3ForcePathStyle = aws.Bool(true)
	}

	// Create session
	sess, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS session: %w", err)
	}

	// Create S3 client
	s3Client := s3.New(sess)

	dest := &S3Destination{
		config:   config,
		s3Client: s3Client,
	}

	log.Printf("[S3Dest] Initialized S3 destination: bucket=%s, region=%s", 
		config.S3Bucket, config.S3Region)

	return dest, nil
}

// Upload uploads a backup file to S3
func (sd *S3Destination) Upload(filename string, reader io.Reader, sizeBytes int64) error {
	key := path.Join(sd.config.Path, filename)
	log.Printf("[S3Dest] Uploading %s to s3://%s/%s (%d bytes)", 
		filename, sd.config.S3Bucket, key, sizeBytes)

	// Read all data into memory (required for S3 PutObject)
	// For large files, consider using multipart upload
	data, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("failed to read data: %w", err)
	}

	// Upload to S3
	_, err = sd.s3Client.PutObject(&s3.PutObjectInput{
		Bucket:        aws.String(sd.config.S3Bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(sizeBytes),
		ContentType:   aws.String("application/gzip"),
		StorageClass:  aws.String("STANDARD"),
	})

	if err != nil {
		return fmt.Errorf("failed to upload to S3: %w", err)
	}

	log.Printf("[S3Dest] Upload complete: %s", filename)
	return nil
}

// Download downloads a backup file from S3
func (sd *S3Destination) Download(filename string, writer io.Writer) error {
	key := path.Join(sd.config.Path, filename)
	log.Printf("[S3Dest] Downloading %s from s3://%s/%s", 
		filename, sd.config.S3Bucket, key)

	// Get object from S3
	result, err := sd.s3Client.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(sd.config.S3Bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return fmt.Errorf("failed to get object from S3: %w", err)
	}
	defer result.Body.Close()

	// Copy data to writer
	if _, err := io.Copy(writer, result.Body); err != nil {
		return fmt.Errorf("failed to read S3 object: %w", err)
	}

	log.Printf("[S3Dest] Download complete: %s", filename)
	return nil
}

// Delete removes a backup file from S3
func (sd *S3Destination) Delete(filename string) error {
	key := path.Join(sd.config.Path, filename)
	log.Printf("[S3Dest] Deleting s3://%s/%s", sd.config.S3Bucket, key)

	_, err := sd.s3Client.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(sd.config.S3Bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return fmt.Errorf("failed to delete from S3: %w", err)
	}

	log.Printf("[S3Dest] Delete complete: %s", filename)
	return nil
}

// List returns all backup files in the S3 destination
func (sd *S3Destination) List() ([]BackupFile, error) {
	prefix := sd.config.Path
	if prefix != "" && !path.IsAbs(prefix) {
		prefix = prefix + "/"
	}

	log.Printf("[S3Dest] Listing objects with prefix: %s", prefix)

	result, err := sd.s3Client.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket: aws.String(sd.config.S3Bucket),
		Prefix: aws.String(prefix),
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list S3 objects: %w", err)
	}

	var files []BackupFile
	for _, obj := range result.Contents {
		// Skip directories
		if *obj.Key == prefix || *obj.Key == prefix+"/" {
			continue
		}

		filename := path.Base(*obj.Key)
		files = append(files, BackupFile{
			Filename:  filename,
			SizeBytes: *obj.Size,
			CreatedAt: obj.LastModified.Unix(),
		})
	}

	return files, nil
}

// GetType returns the destination type
func (sd *S3Destination) GetType() string {
	return "s3"
}
