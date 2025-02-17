package tos

import (
	"context"
	"encoding/json"
	"hash"
	"io"
	"io/ioutil"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/volcengine/ve-tos-golang-sdk/v2/tos/enum"
)

type multipartUpload struct {
	Bucket   string `json:"Bucket,omitempty"`
	Key      string `json:"Key,omitempty"`
	UploadID string `json:"UploadId,omitempty"`
}

type uploadedPart struct {
	PartNumber int    `json:"PartNumber"`
	ETag       string `json:"ETag"`
}

type uploadedParts []uploadedPart

func (p uploadedParts) Less(i, j int) bool { return p[i].PartNumber < p[j].PartNumber }
func (p uploadedParts) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func (p uploadedParts) Len() int           { return len(p) }

// only for marshal
type partsToComplete struct {
	Parts uploadedParts `json:"Parts"`
}

// only for Marshal
type deleteMultiObjectsInput struct {
	Objects []ObjectTobeDeleted `json:"Objects,omitempty"`
	Quiet   bool                `json:"Quiet,omitempty"`
}

// only for Marshal
type accessControlList struct {
	Owner  Owner
	Grants []Grant
}

type canceler struct {
	called       int32
	cancelHandle chan struct{}
	// cleaner will clean all files need to be deleted
	cleaner func()
	// aborter will  abort multi upload task
	aborter func() error
}

func (c *canceler) Cancel(isAbort bool) {
	if c.cancelHandle == nil {
		return
	}
	if atomic.CompareAndSwapInt32(&c.called, 0, 1) {
		if isAbort {
			if c.cleaner != nil {
				c.cleaner()
			}
			if c.aborter != nil {
				c.aborter()
			}
		}
		close(c.cancelHandle)
	}
}

// do nothing
func (c *canceler) internal() {}

type downloadObjectInfo struct {
	Etag          string    `json:"Etag,omitempty"`
	HashCrc64ecma uint64    `json:"HashCrc64Ecma,omitempty"`
	LastModified  time.Time `json:"LastModified,omitempty"`
	ObjectSize    int64     `json:"ObjectSize,omitempty"`
}

type downloadFileInfo struct {
	FilePath     string `json:"FilePath,omitempty"`
	TempFilePath string `json:"TempFilePath,omitempty"`
}

// downloadPartInfo is for checkpoint
type downloadPartInfo struct {
	PartNumber    int    `json:"PartNumber,omitempty"`
	RangeStart    int64  `json:"RangeStart"` // not omit empty
	RangeEnd      int64  `json:"RangeEnd,omitempty"`
	HashCrc64ecma uint64 `json:"HashCrc64Ecma,omitempty"`
	IsCompleted   bool   `json:"IsCompleted"` // not omit empty
}

type downloadCheckpoint struct {
	checkpointPath string // this filed should not be marshaled
	Bucket         string `json:"Bucket,omitempty"`
	Key            string `json:"Key,omitempty"`
	VersionID      string `json:"VersionID,omitempty"`
	PartSize       int64  `json:"PartSize,omitempty"`

	IfMatch           string    `json:"IfMatch,omitempty"`
	IfModifiedSince   time.Time `json:"IfModifiedSince,omitempty"`
	IfNoneMatch       string    `json:"IfNoneMatch,omitempty"`
	IfUnmodifiedSince time.Time `json:"IfUnmodifiedSince,omitempty"`

	SSECAlgorithm string             `json:"SSECAlgorithm,omitempty"`
	SSECKeyMD5    string             `json:"SSECKeyMD5,omitempty"`
	ObjectInfo    downloadObjectInfo `json:"ObjectInfo,omitempty"`
	FileInfo      downloadFileInfo   `json:"FileInfo,omitempty"`
	PartsInfo     []downloadPartInfo `json:"PartsInfo,omitempty"`
}

func (c *downloadCheckpoint) WriteToFile() error {
	buffer, err := json.Marshal(c)
	if err != nil {
		return newTosClientError(err.Error(), err)
	}
	err = ioutil.WriteFile(c.checkpointPath, buffer, 0666)
	if err != nil {
		return newTosClientError(err.Error(), err)
	}
	return nil
}

func (c *downloadCheckpoint) Valid(input *DownloadFileInput, head *HeadObjectV2Output) bool {
	if c.Bucket != input.Bucket || c.Key != input.Key || c.VersionID != input.VersionID || c.PartSize != input.PartSize ||
		c.IfMatch != input.IfMatch || c.IfModifiedSince != input.IfModifiedSince || c.IfNoneMatch != input.IfNoneMatch ||
		c.IfUnmodifiedSince != input.IfUnmodifiedSince ||
		c.SSECAlgorithm != input.SSECAlgorithm || c.SSECKeyMD5 != input.SSECKeyMD5 {
		return false
	}

	if c.ObjectInfo.Etag != head.ETag || c.ObjectInfo.HashCrc64ecma != head.HashCrc64ecma ||
		c.ObjectInfo.LastModified != head.LastModified || c.ObjectInfo.ObjectSize != head.ContentLength {
		return false
	}
	if c.FileInfo.FilePath != input.FilePath {
		return false
	}
	return true
}

func (c *downloadCheckpoint) UpdatePartsInfo(part downloadPartInfo) {
	c.PartsInfo[part.PartNumber-1] = part
}

type fileInfo struct {
	LastModified int64 `json:"LastModified,omitempty"`
	Size         int64 `json:"Size"`
}

// uploadPartInfo is for checkpoint
type uploadPartInfo struct {
	uploadID      *string // should not be marshaled
	PartNumber    int     `json:"PartNumber"`
	PartSize      int64   `json:"PartSize"`
	Offset        uint64  `json:"Offset"`
	ETag          string  `json:"ETag,omitempty"`
	HashCrc64ecma uint64  `json:"HashCrc64Ecma,omitempty"`
	IsCompleted   bool    `json:"IsCompleted"`
}

type uploadCheckpoint struct {
	checkpointPath string           // this filed should not be marshaled
	Bucket         string           `json:"Bucket,omitempty"`
	Key            string           `json:"Key,omitempty"`
	UploadID       string           `json:"UploadID,omitempty"`
	PartSize       int64            `json:"PartSize"`
	SSECAlgorithm  string           `json:"SSECAlgorithm,omitempty"`
	SSECKeyMD5     string           `json:"SSECKeyMD5,omitempty"`
	EncodingType   string           `json:"EncodingType,omitempty"`
	FilePath       string           `json:"FilePath,omitempty"`
	FileInfo       fileInfo         `json:"FileInfo"`
	PartsInfo      []uploadPartInfo `json:"PartsInfo,omitempty"`
}

func (u *uploadCheckpoint) Valid(uploadFileStat os.FileInfo, bucketName, key, uploadFile string) bool {
	if u.UploadID == "" || u.Bucket != bucketName || u.Key != key || u.FilePath != uploadFile {
		return false
	}
	if u.FileInfo.Size != uploadFileStat.Size() || u.FileInfo.LastModified != uploadFileStat.ModTime().Unix() {
		return false
	}
	return true
}

func (u *uploadCheckpoint) GetParts() []UploadedPartV2 {
	parts := make([]UploadedPartV2, 0, len(u.PartsInfo))
	for _, p := range u.PartsInfo {
		parts = append(parts, UploadedPartV2{
			PartNumber: p.PartNumber,
			ETag:       p.ETag,
		})
	}
	return parts
}

func (u *uploadCheckpoint) UpdatePartsInfo(part uploadPartInfo) {
	u.PartsInfo[part.PartNumber-1] = part
}

func (u *uploadCheckpoint) WriteToFile() error {
	result, err := json.Marshal(u)
	if err != nil {
		return newTosClientError(err.Error(), err)
	}
	err = ioutil.WriteFile(u.checkpointPath, result, 0666)
	if err != nil {
		return newTosClientError(err.Error(), err)
	}
	return nil
}

/*
   taskManager basic usage
   manager:=newTaskManager(n)
   // call taskManager.run before adding tasks
   manager.run()
   manger.addTask(tasks)
   // call taskManager.finishAdd once you finish adding tasks
   manager.finishAdd()
*/

type task interface {
	do() (interface{}, error)
	getBaseInput() interface{}
}

//type downloadTask struct {
//	cli        *ClientV2
//	ctx        context.Context
//	input      *DownloadFileInput
//	consumed   *int64
//	total      int64
//	mutex      *sync.Mutex
//	PartNumber int
//	RangeStart int64
//	RangeEnd   int64
//}
//
//// Do the downloadTask, and return downloadPartInfo
//func (t *downloadTask) do() (result interface{}, err error) {
//	input := t.getBaseInput().(GetObjectV2Input)
//	output, err := t.cli.GetObjectV2(t.ctx, &input)
//	if err != nil {
//		return nil, err
//	}
//	file, err := os.OpenFile(t.input.tempFile, os.O_RDWR, 0)
//	if err != nil {
//		return nil, err
//	}
//	defer func(file *os.File) {
//		_ = file.Close()
//	}(file)
//	var wrapped = output.Content
//	if t.input.DataTransferListener != nil {
//		wrapped = &parallelReadCloserWithListener{
//			listener: t.input.DataTransferListener,
//			base:     wrapped,
//			consumed: t.consumed,
//			total:    t.total,
//			m:        t.mutex,
//		}
//	}
//	if t.input.RateLimiter != nil {
//		wrapped = &ReadCloserWithLimiter{
//			limiter: t.input.RateLimiter,
//			base:    wrapped,
//		}
//	}
//	_, err = file.Seek(t.RangeStart, io.SeekStart)
//	if err != nil {
//		return nil, err
//	}
//	written, err := io.Copy(file, wrapped)
//	if err != nil {
//		return nil, err
//	}
//	if written != (t.RangeEnd - t.RangeStart + 1) {
//		return nil, err
//	}
//	return downloadPartInfo{
//		PartNumber:    t.PartNumber,
//		RangeStart:    t.RangeStart,
//		RangeEnd:      t.RangeEnd,
//		HashCrc64ecma: output.HashCrc64ecma,
//		IsCompleted:   true,
//	}, nil
//}
//
//func (t *downloadTask) getBaseInput() interface{} {
//	return GetObjectV2Input{
//		Bucket:            t.input.Bucket,
//		Key:               t.input.Key,
//		VersionID:         t.input.VersionID,
//		IfMatch:           t.input.IfMatch,
//		IfModifiedSince:   t.input.IfModifiedSince,
//		IfNoneMatch:       t.input.IfNoneMatch,
//		IfUnmodifiedSince: t.input.IfUnmodifiedSince,
//		SSECAlgorithm:     t.input.SSECAlgorithm,
//		SSECKey:           t.input.SSECKey,
//		SSECKeyMD5:        t.input.SSECKeyMD5,
//		RangeStart:        t.RangeStart,
//		RangeEnd:          t.RangeEnd,
//		// we want to Sent parallel Listener on output, so explicitly set listener of GetObjectV2Input nil here.
//		DataTransferListener: nil,
//		RateLimiter:          nil,
//	}
//}

type uploadTask struct {
	cli        *ClientV2
	input      *UploadFileInput
	consumed   *int64
	subtotal   *int64
	mutex      *sync.Mutex
	ctx        context.Context
	total      int64
	UploadID   string
	ContentMD5 string
	PartNumber int
	Offset     uint64
	PartSize   int64
}

// Do the uploadTask, and return uploadPartInfo
func (t *uploadTask) do() (interface{}, error) {
	file, err := os.Open(t.input.FilePath)
	if err != nil {
		return nil, newTosClientError(err.Error(), err)
	}
	_, err = file.Seek(int64(t.Offset), io.SeekStart)
	if err != nil {
		return nil, newTosClientError(err.Error(), err)
	}
	var wrapped = ioutil.NopCloser(io.LimitReader(file, t.input.PartSize))
	if t.input.DataTransferListener != nil {
		wrapped = &parallelReadCloserWithListener{
			listener: t.input.DataTransferListener,
			base:     wrapped,
			total:    t.total,
			subtotal: t.subtotal,
			consumed: t.consumed,
		}
	}
	if t.input.RateLimiter != nil {
		wrapped = &ReadCloserWithLimiter{
			limiter: t.input.RateLimiter,
			base:    wrapped,
		}
	}
	input := t.getBaseInput().(UploadPartV2Input)
	input.Content = wrapped
	output, err := t.cli.UploadPartV2(t.ctx, &UploadPartV2Input{
		UploadPartBasicInput: input.UploadPartBasicInput,
		Content:              wrapped,
		ContentLength:        input.ContentLength,
	})
	if err != nil {
		return nil, err
	}
	return uploadPartInfo{
		uploadID:      &t.UploadID,
		PartNumber:    output.PartNumber,
		PartSize:      t.PartSize,
		Offset:        t.Offset,
		ETag:          output.ETag,
		HashCrc64ecma: output.HashCrc64ecma,
		IsCompleted:   true,
	}, nil
}

func (t *uploadTask) getBaseInput() interface{} {
	return UploadPartV2Input{
		UploadPartBasicInput: UploadPartBasicInput{
			Bucket:               t.input.Bucket,
			Key:                  t.input.Key,
			UploadID:             t.UploadID,
			PartNumber:           t.PartNumber,
			ContentMD5:           t.ContentMD5,
			SSECAlgorithm:        t.input.SSECAlgorithm,
			SSECKey:              t.input.SSECKey,
			SSECKeyMD5:           t.input.SSECKeyMD5,
			ServerSideEncryption: t.input.ServerSideEncryption,
		},
		ContentLength: t.PartSize,
	}
}

type retryAction int

const (
	NoRetry retryAction = iota
	Retry
)

const (
	DefaultRetryBackoffBase = 100 * time.Millisecond
)

type classifier interface {
	Classify(error) retryAction
}

func exponentialBackoff(n int, base time.Duration) []time.Duration {
	backoffs := make([]time.Duration, n)
	for i := 0; i < len(backoffs); i++ {
		backoffs[i] = base
		base *= 1
	}
	return backoffs
}

type retryer struct {
	backoff []time.Duration
	jitter  float64
}

func (r *retryer) SetBackoff(backoff []time.Duration) {
	r.backoff = backoff
}

// newRetryer constructs a retryer with the given backoff pattern and classifier. The length of the backoff pattern
// indicates how many times an action will be retried, and the value at each index indicates the amount of time
// waited before each subsequent retry. The classifier is used to determine which errors should be retried and
// which should cause the retrier to fail fast. The DefaultClassifier is used if nil is passed.
func newRetryer(backoff []time.Duration) *retryer {
	return &retryer{
		backoff: backoff,
	}
}

func worthToRetry(ctx context.Context, waitTime time.Duration) bool {
	if ctx == nil {
		return true
	}
	if ctx.Err() != nil {
		return false
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return true
	}
	now := time.Now()
	return now.UnixNano()+int64(waitTime) <= deadline.UnixNano()
}

// Run executes the given work function, then classifies its return value based on the classifier.
// If the result is Succeed or Fail, the return value of the work function is
// returned to the caller. If the result is Retry, then Run sleeps according to its backoff policy
// before retrying. If the total number of retries is exceeded then the return value of the work function
// is returned to the caller regardless.
func (r *retryer) Run(ctx context.Context, work func() error, classifier classifier) error {
	// run
	ferr := work()
	// try retry
	for i := 0; i < len(r.backoff) && classifier.Classify(ferr) == Retry; i++ {
		// 重试
		sleepTime := r.calcSleep(i)
		if !worthToRetry(ctx, sleepTime) {
			return ferr
		}
		time.Sleep(sleepTime)
		ferr = work()
	}
	return ferr
}

func (r *retryer) calcSleep(i int) time.Duration {
	// take a random float in the range (-r.jitter, +r.jitter) and multiply it by the base amount
	return r.backoff[i]
}

// SetJitter sets the amount of jitter on each back-off to a factor between 0.0 and 1.0 (values outside this range
// are silently ignored). When a retry occurs, the back-off is adjusted by a random amount up to this value.
func (r *retryer) SetJitter(jit float64) {
	if jit < 0 || jit > 1 {
		return
	}
	r.jitter = jit
}

// readCloserWithCRC warp io.ReadCloser with crc checker
type readCloserWithCRC struct {
	checker hash.Hash64
	base    io.ReadCloser
}

func (r *readCloserWithCRC) Read(p []byte) (n int, err error) {
	n, err = r.base.Read(p)
	if n > 0 {
		if n, err = r.checker.Write(p[:n]); err != nil {
			return n, err
		}
	}
	return
}

func (r *readCloserWithCRC) Close() error {
	return r.base.Close()
}

// parallelReadCloserWithListener warp multiple io.ReadCloser will be R/W in parallel with a same DataTransferListener
type parallelReadCloserWithListener struct {
	listener DataTransferListener
	base     io.ReadCloser
	consumed *int64
	subtotal *int64
	total    int64
	m        *sync.Mutex
}

func (r *parallelReadCloserWithListener) Read(p []byte) (n int, err error) {
	n, err = r.base.Read(p)
	if err != nil && err != io.EOF {
		postDataTransferStatus(r.listener, &DataTransferStatus{
			Type: enum.DataTransferFailed,
		})
		return n, err
	}
	if n <= 0 {
		return
	}
	consumed := atomic.AddInt64(r.consumed, int64(n))
	subtotal := atomic.AddInt64(r.subtotal, int64(n))
	if subtotal >= 4*1024*1024 {
		postDataTransferStatus(r.listener, &DataTransferStatus{
			Type:          enum.DataTransferRW,
			RWOnceBytes:   subtotal,
			ConsumedBytes: consumed,
			TotalBytes:    r.total,
		})
		atomic.StoreInt64(r.subtotal, 0)
	}
	if consumed == r.total {
		if subtotal < 4*1024*1024 {
			postDataTransferStatus(r.listener, &DataTransferStatus{
				Type:          enum.DataTransferRW,
				RWOnceBytes:   subtotal,
				ConsumedBytes: consumed,
				TotalBytes:    r.total,
			})
		}
		postDataTransferStatus(r.listener, &DataTransferStatus{
			Type:          enum.DataTransferSucceed,
			ConsumedBytes: consumed,
			TotalBytes:    r.total,
		})
	}
	return
}

func (r *parallelReadCloserWithListener) Close() error {
	return r.base.Close()
}

// readCloserWithListener warp io.ReadCloser with DataTransferListener
type readCloserWithListener struct {
	listener DataTransferListener
	base     io.ReadCloser
	consumed int64
	total    int64
}

func (r *readCloserWithListener) Read(p []byte) (n int, err error) {
	if r.consumed == 0 {
		postDataTransferStatus(r.listener, &DataTransferStatus{
			Type: enum.DataTransferStarted,
		})
	}
	n, err = r.base.Read(p)
	if err != nil && err != io.EOF {
		postDataTransferStatus(r.listener, &DataTransferStatus{
			Type: enum.DataTransferFailed,
		})
		return n, err
	}
	if n <= 0 {
		return
	}
	r.consumed += int64(n)
	postDataTransferStatus(r.listener, &DataTransferStatus{
		Type:          enum.DataTransferRW,
		RWOnceBytes:   int64(n),
		ConsumedBytes: r.consumed,
		TotalBytes:    r.total,
	})
	if r.consumed == r.total {
		postDataTransferStatus(r.listener, &DataTransferStatus{
			Type:          enum.DataTransferSucceed,
			ConsumedBytes: r.consumed,
			TotalBytes:    r.total,
		})
	}
	return
}

func (r *readCloserWithListener) Close() error {
	return r.base.Close()
}

// ReadCloserWithLimiter warp io.ReadCloser with DataTransferListener
type ReadCloserWithLimiter struct {
	limiter RateLimiter
	base    io.ReadCloser
}

func (r ReadCloserWithLimiter) Read(p []byte) (n int, err error) {
	want := len(p)
	for {
		ok, timeToWait := r.limiter.Acquire(int64(want))
		if ok {
			break
		}
		time.Sleep(timeToWait)
	}
	return r.base.Read(p)
}

func (r ReadCloserWithLimiter) Close() error {
	return r.base.Close()
}
