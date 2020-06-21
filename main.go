package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/aws/aws-sdk-go/service/sns"
	"github.com/aws/aws-sdk-go/service/sqs"

	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"google.golang.org/api/youtube/v3"
)

var awsSession *session.Session
var sqsService *sqs.SQS
var s3Downloader *s3manager.Downloader

var qURL = "https://sqs.us-east-1.amazonaws.com/776913033148/moments.fifo"
var qBucket = "upload.momentsfrom.earth"
var archiveBucket = "archive.momentsfrom.earth"

var jobCompletedTopic = "arn:aws:sns:us-east-1:776913033148:MomJobDone"
var jobFailedTopic = "arn:aws:sns:us-east-1:776913033148:MomJobFail"

var currMomentID string

var creds *credentials

type moment struct {
	MFEKey      string `json:"mfe_key"`
	MomentID    string `json:"moment_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Filename    string `json:"filename"`
	Type        string `json:"type"`
	Size        int32  `json:"size"`
	QueueID     string `json:"queue_id"`
	Status      string `json:"status"`
	Creator     string `json:"creator"`
	Created     int64  `json:"created"`
	Updated     int64  `json:"updated"`
	HostID      string `json:"host_id"`
}

type credentials struct {
	MFE struct {
		APIKey string `json:"api_key"`
	}
	YouTube struct {
		Scopes       []string `json:"scopes"`
		ClientID     string   `json:"client_id"`
		ClientSecret string   `json:"client_secret"`
		RedirectURL  string   `json:"redirect_url"`
		RefreshToken string   `json:"refresh_token"`
	}
}

func uploadToYouTube(momentID string, videoData *aws.WriteAtBuffer) (string, error) {
	ctx := context.Background()

	config := &oauth2.Config{
		ClientID:     creds.YouTube.ClientID,
		ClientSecret: creds.YouTube.ClientSecret,
		Scopes:       creds.YouTube.Scopes,
		RedirectURL:  creds.YouTube.RedirectURL,
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/auth",
			TokenURL: "https://accounts.google.com/o/oauth2/token",
		},
	}

	client := config.Client(ctx, &oauth2.Token{
		RefreshToken: creds.YouTube.RefreshToken,
	})

	service, err := youtube.New(client)
	if err != nil {
		return "", err
	}

	var title = momentID
	var category = "1" // film and animation
	var privacy = "unlisted"
	var keywords = "momentsfromearth,moment"

	upload := &youtube.Video{
		Snippet: &youtube.VideoSnippet{
			Title:      title,
			CategoryId: category,
		},
		Status: &youtube.VideoStatus{PrivacyStatus: privacy},
	}
	if strings.Trim(keywords, "") != "" {
		upload.Snippet.Tags = strings.Split(keywords, ",")
	}

	call := service.Videos.Insert([]string{"snippet", "status"}, upload)
	file := bytes.NewReader(videoData.Bytes())

	res, err := call.Media(file).Do()

	return res.Id, err
}

func downloadVideo(queueID string) (*aws.WriteAtBuffer, error) {
	videoData := aws.WriteAtBuffer{}
	_, err := s3Downloader.Download(&videoData, &s3.GetObjectInput{
		Bucket: aws.String(qBucket),
		Key:    aws.String(queueID),
	})
	return &videoData, err
}

func archiveVideo(queueID string) (*s3.CopyObjectOutput, error) {
	s3Service := s3.New(awsSession)
	uploadVideo := fmt.Sprintf("%v/%v", qBucket, queueID)
	return s3Service.CopyObject(&s3.CopyObjectInput{
		CopySource: aws.String(uploadVideo),
		Bucket:     aws.String(archiveBucket),
		Key:        aws.String(queueID),
	})
}

func deleteVideo(queueID string) (*s3.DeleteObjectOutput, error) {
	s3Service := s3.New(awsSession)
	return s3Service.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(qBucket),
		Key:    aws.String(queueID),
	})
}

func deleteMomentJob(jobHandle string) (*sqs.DeleteMessageOutput, error) {
	return sqsService.DeleteMessage(&sqs.DeleteMessageInput{
		QueueUrl:      &qURL,
		ReceiptHandle: &jobHandle,
	})
}

func updateMomentData(m *moment) error {
	updatedMoment, err := json.Marshal(m)
	check(err, "There was a problem creating update moment json")
	fmt.Println(string(updatedMoment))
	url := fmt.Sprintf("https://api.momentsfrom.earth/moment/%v/callback?api_key=%v", m.MomentID, creds.MFE.APIKey)
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewBuffer(updatedMoment))
	check(err, "There was a problem updating the moment")
	req.Header.Add("Content-Type", "application/json")
	client := &http.Client{}
	res, err := client.Do(req)
	if res.StatusCode != 200 {
		fmt.Println("Invalid StatusCode from Moment Update", res.StatusCode)
	}
	return nil
}

func sendNotification(topic string, msg string) (*sns.PublishOutput, error) {
	snsService := sns.New(awsSession)
	return snsService.Publish(&sns.PublishInput{
		TopicArn: &topic,
		Message:  &msg,
	})
}

func processMomentJob(m *moment, jobHandle string) error {
	fmt.Println("")
	fmt.Println("Processing Moment")
	fmt.Println(m.MomentID)
	fmt.Println(m.QueueID)
	fmt.Println("")

	currMomentID = m.MomentID

	fmt.Println("Update moment status to Processing: Start")
	m.Status = "processing"
	m.HostID = "youtube:tbd"
	err := updateMomentData(m)
	check(err, "There was a problem updating moment status to Processing")
	fmt.Println("Update moment status to Processing: Done")

	fmt.Println("Download video from S3: Start")
	videoData, err := downloadVideo(m.QueueID)
	check(err, "There was a problem downloading video from S3")
	fmt.Println("Download video from S3: Done")

	fmt.Println("Upload video to YouTube: Start")
	youtubeID, err := uploadToYouTube(m.MomentID, videoData)
	check(err, "Error uploading video to YouTube")
	fmt.Println(youtubeID)
	fmt.Println("Upload video to YouTube: Done")

	fmt.Println("Copy original video file to S3 archive: Start")
	archiveRes, err := archiveVideo(m.QueueID)
	check(err, "There was a problem archiving video")
	fmt.Println(archiveRes)
	fmt.Println("Copy original video file to S3 archive: Done")

	fmt.Println("Delete original video from S3 upload: Start")
	deleteRes, err := deleteVideo(m.QueueID)
	check(err, "There was a problem deleting video")
	fmt.Println(deleteRes)
	fmt.Println("Delete original video from S3 upload: Done")

	fmt.Println("Delete video job from SQS queue: Start")
	deleteJobRes, err := deleteMomentJob(jobHandle)
	check(err, "There was a problem deleting the video job")
	fmt.Println(deleteJobRes)
	fmt.Println("Delete video job from SQS queue: Done")

	fmt.Println("Update moment status to InReview, set host_id: Start")
	m.Status = "inreview"
	m.HostID = fmt.Sprintf("youtube:%v", youtubeID)
	err = updateMomentData(m)
	check(err, "There was a problem updating moment status to Processing")
	fmt.Println("Update moment status to InReview, set host_id: Done")

	fmt.Println("Send SNS Notification of Success: Start")
	topicRes, err := sendNotification(jobCompletedTopic, m.MomentID)
	check(err, "There was a problem sending job complete SNS notification")
	fmt.Println(topicRes)
	fmt.Println("Send SNS Notification of Success: Done")

	return nil
}

func fetchMomentJob() (*sqs.ReceiveMessageOutput, error) {
	return sqsService.ReceiveMessage(&sqs.ReceiveMessageInput{
		QueueUrl:            &qURL,
		MaxNumberOfMessages: aws.Int64(10),
		WaitTimeSeconds:     aws.Int64(1),
	})
}

func parseMoment(js string) *moment {
	m := moment{}
	json.Unmarshal([]byte(js), &m)
	return &m
}

func runMomentProcessor() {
	res, _ := fetchMomentJob()
	if len(res.Messages) > 0 {
		for _, msg := range res.Messages {
			h := *msg.ReceiptHandle
			m := parseMoment(*msg.Body)
			processMomentJob(m, h)
		}
		runMomentProcessor()
	} else {
		res, _ := sqsService.GetQueueAttributes(&sqs.GetQueueAttributesInput{
			QueueUrl:       &qURL,
			AttributeNames: aws.StringSlice([]string{"All"}),
		})
		numMsgs, _ := strconv.Atoi(*res.Attributes["ApproximateNumberOfMessages"])
		if numMsgs > 0 {
			fmt.Println("Available messages is greater than 0, retrying...")
			runMomentProcessor()
		} else {
			fmt.Println("No more messages!")
			os.Exit(0)
		}
	}
}

func initAWSSession() (*session.Session, error) {
	return session.NewSession(&aws.Config{Region: aws.String("us-east-1")})
}

func initSQSService(session *session.Session) *sqs.SQS {
	return sqs.New(session)
}

func initS3Downloader(session *session.Session) *s3manager.Downloader {
	return s3manager.NewDownloader(session)
}

func readCredentials() *credentials {
	credsJSON, err := ioutil.ReadFile("./creds.json")
	check(err, "Could not read creds.json")
	creds := &credentials{}
	err = json.Unmarshal([]byte(credsJSON), creds)
	check(err, "Could not parse creds.json")
	return creds
}

func main() {
	creds = readCredentials()
	awsSession, _ = initAWSSession()
	sqsService = initSQSService(awsSession)
	s3Downloader = initS3Downloader(awsSession)
	runMomentProcessor()
}

func check(err error, msg string) {
	if err != nil {
		fmt.Println(err)
		fmt.Println("Send SNS Notification of Failure: Start")
		topicRes, _ := sendNotification(jobFailedTopic, currMomentID)
		fmt.Println("Send SNS Notification of Failure: Done")
		fmt.Println(topicRes)
		os.Exit(1)
	}
}
