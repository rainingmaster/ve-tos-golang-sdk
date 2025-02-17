package tos

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

// Client TOS Client
// use NewClient to create a new Client
//
// example:
//   client, err := NewClient(endpoint, WithCredentials(credentials), WithRegion(region))
//   if err != nil {
//      // ...
//   }
//   // do something
//
// if you only access the public bucket:
//   client, err := NewClient(endpoint)
//   // do something
//
// Deprecated: use ClientV2 instead
type Client struct {
	scheme       string
	host         string
	urlMode      urlMode
	userAgent    string
	credentials  Credentials // nullable
	signer       Signer      // nullable
	transport    Transport
	recognizer   ContentTypeRecognizer
	config       Config
	retry        *retryer
	dnsCacheTime time.Duration // milliseconds
	enableCRC    bool
	proxy        *Proxy
}

// ClientV2 TOS ClientV2
// use NewClientV2 to create a new ClientV2
//
// example:
//   client, err := NewClientV2(endpoint, WithCredentials(credentials), WithRegion(region))
//   if err != nil {
//      // ...
//   }
//   // do something
//
// if you only access the public bucket:
//   client, err := NewClientV2(endpoint)
//   // do something
//
type ClientV2 struct {
	Client
}

type ClientOption func(*Client)

// WithCredentials set Credentials
//
// see StaticCredentials, WithoutSecretKeyCredentials and FederationCredentials
func WithCredentials(credentials Credentials) ClientOption {
	return func(client *Client) {
		client.credentials = credentials
	}
}

// WithEnableVerifySSL set whether a client verifies the server's certificate chain and host name.
func WithEnableVerifySSL(enable bool) ClientOption {
	skip := !enable
	return func(client *Client) {
		client.config.TransportConfig.InsecureSkipVerify = skip
	}
}

// WithRequestTimeout set timeout for single http request
func WithRequestTimeout(timeout time.Duration) ClientOption {
	return func(client *Client) {
		client.config.TransportConfig.ResponseHeaderTimeout = timeout

	}
}

// WithConnectionTimeout set timeout for constructing connection
func WithConnectionTimeout(timeout time.Duration) ClientOption {
	return func(client *Client) {
		client.config.TransportConfig.DialTimeout = timeout
	}
}

// WithMaxConnections set maximum number of http connections
func WithMaxConnections(max int) ClientOption {
	return func(client *Client) {
		client.config.TransportConfig.MaxIdleConns = max
	}

}

// WithIdleConnTimeout set max idle time of a http connection
func WithIdleConnTimeout(timeout time.Duration) ClientOption {
	return func(client *Client) {
		client.config.TransportConfig.IdleConnTimeout = timeout
	}
}

// WithUserAgentSuffix set suffix of user-agent
func WithUserAgentSuffix(suffix string) ClientOption {
	return func(client *Client) {
		client.userAgent = strings.Join([]string{client.userAgent, suffix}, " ")
	}
}

// // WithProxy set Proxy
// //
// // see StaticProxy
// func WithProxy(proxy *Proxy) ClientV2Option {
//	return func(client *ClientV2) {
//		client.proxy = proxy
//	}
// }
//
// // WithDNSCacheTime set dnsCacheTime in milliseconds
// func WithDNSCacheTime(dnsCacheTime int) ClientV2Option {
//	return func(client *ClientV2) {
//		client.dnsCacheTime = dnsCacheTime * time.Milliseconds
//	}
// }
//

// WithEnableCRC set if check crc after uploading object.
// Checking crc is enabled by default.
// func WithEnableCRC(enableCRC bool) ClientOption {
// 	return func(client *Client) {
// 		client.enableCRC = enableCRC
// 	}
// }

// // WithMaxRetryCount set MaxRetryCount
// func WithMaxRetryCount(retryCount int) ClientOption {
// 	return func(client *Client) {
// 		if client.retry != nil {
// 			client.retry.SetBackoff(retryer.exponentialBackoff(retryCount, retryer.DefaultRetryBackoffBase))
// 		}
// 	}
// }

// WithTransport set Transport
func WithTransport(transport Transport) ClientOption {
	return func(client *Client) {
		client.transport = transport
	}
}

// WithTransportConfig set TransportConfig
func WithTransportConfig(config *TransportConfig) ClientOption {
	return func(client *Client) {
		// client.config never be nil
		client.config.TransportConfig = *config
	}
}

// WithSocketTimeout set read-write timeout
func WithSocketTimeout(readTimeout, writeTimeout time.Duration) ClientOption {
	return func(client *Client) {
		client.config.TransportConfig.ReadTimeout = readTimeout
		client.config.TransportConfig.WriteTimeout = writeTimeout
	}
}

// WithRegion set region
func WithRegion(region string) ClientOption {
	return func(client *Client) {
		// client.config never be nil
		client.config.Region = region
		if endpoint, ok := SupportedRegion()[region]; ok {
			client.config.Endpoint = endpoint
		}
	}
}

// WithSigner for self-defined Signer
func WithSigner(signer Signer) ClientOption {
	return func(client *Client) {
		client.signer = signer
	}
}

// WithPathAccessMode url mode is path model or default mode
//
// Deprecated: This option is deprecated. Setting PathAccessMode will be ignored silently.
func WithPathAccessMode(pathAccessMode bool) ClientOption {
	return func(client *Client) {
	}
}

// WithAutoRecognizeContentType set to recognize Content-Type or not, the default is enabled.
func WithAutoRecognizeContentType(enable bool) ClientOption {
	return func(client *Client) {
		if enable {
			client.recognizer = ExtensionBasedContentTypeRecognizer{}
		} else {
			client.recognizer = EmptyContentTypeRecognizer{}
		}
	}
}

// WithContentTypeRecognizer set ContentTypeRecognizer to recognize Content-Type,
// the default is ExtensionBasedContentTypeRecognizer
func WithContentTypeRecognizer(recognizer ContentTypeRecognizer) ClientOption {
	return func(client *Client) {
		client.recognizer = recognizer
	}
}

func schemeHost(endpoint string) (scheme string, host string, urlMode urlMode) {
	if strings.HasPrefix(endpoint, "https://") {
		scheme = "https"
		host = endpoint[len("https://"):]
	} else if strings.HasPrefix(endpoint, "http://") {
		scheme = "http"
		host = endpoint[len("http://"):]
	} else {
		scheme = "http"
		host = endpoint
	}
	urlMode = urlModeDefault
	if net.ParseIP(host) != nil {
		urlMode = urlModePath
	}
	return scheme, host, urlMode
}

func initClient(client *Client, endpoint string, options ...ClientOption) error {
	for _, option := range options {
		option(client)
	}
	// if Region is set and supported, param "endpoint" will be ignored
	if len(client.config.Endpoint) == 0 {
		client.config.Endpoint = endpoint
	}
	client.scheme, client.host, client.urlMode = schemeHost(client.config.Endpoint)

	if client.transport == nil {
		client.transport = NewDefaultTransport(&client.config.TransportConfig)
	}

	if cred := client.credentials; cred != nil && client.signer == nil {
		if len(client.config.Region) == 0 {
			return newTosClientError("tos: missing Region option", nil)
		}
		client.signer = NewSignV4(cred, client.config.Region)
	}

	return nil
}

// NewClient create a new Tos Client
//   endpoint: access endpoint
//   options: WithCredentials set Credentials
//     WithRegion set region, this is required if WithCredentials is used
//     WithSocketTimeout set read-write timeout
//     WithTransportConfig set TransportConfig
//     WithTransport set self-defined Transport
func NewClient(endpoint string, options ...ClientOption) (*Client, error) {
	client := Client{
		recognizer: ExtensionBasedContentTypeRecognizer{},
		config:     defaultConfig(),
		userAgent:  fmt.Sprintf("tos-go-sdk/%s (%s/%s;%s)", Version, runtime.GOOS, runtime.GOARCH, runtime.Version()),
	}
	err := initClient(&client, endpoint, options...)
	if err != nil {
		return nil, err
	}
	return &client, nil
}

// NewClientV2 create a new Tos ClientV2
//   endpoint: access endpoint
//   options: WithCredentials set Credentials
//     WithRegion set region, this is required if WithCredentials is used.
//     If Region is set and supported, param "endpoint" will be ignored.
//     WithSocketTimeout set read-write timeout
//     WithTransportConfig set TransportConfig
//     WithTransport set self-defined Transport
func NewClientV2(endpoint string, options ...ClientOption) (*ClientV2, error) {
	client := ClientV2{
		Client: Client{
			recognizer: ExtensionBasedContentTypeRecognizer{},
			config:     defaultConfig(),
			retry:      newRetryer([]time.Duration{}),
			userAgent:  fmt.Sprintf("tos-go-sdk/%s (%s/%s;%s)", Version, runtime.GOOS, runtime.GOARCH, runtime.Version()),
			// TODO: uncomment this in 2.2.0
			// enableCRC:  true,
		},
	}
	client.retry.SetJitter(0.25)
	err := initClient(&client.Client, endpoint, options...)
	if err != nil {
		return nil, err
	}
	return &client, nil
}

func (cli *Client) newBuilder(bucket, object string, options ...Option) *requestBuilder {
	rb := &requestBuilder{
		Signer:     cli.signer,
		Scheme:     cli.scheme,
		Host:       cli.host,
		Bucket:     bucket,
		Object:     object,
		URLMode:    cli.urlMode,
		Query:      make(url.Values),
		Header:     make(http.Header),
		OnRetry:    func(req *Request) {},
		Classifier: StatusCodeClassifier{},
	}
	rb.Header.Set(HeaderUserAgent, cli.userAgent)
	if typ := cli.recognizer.ContentType(object); len(typ) > 0 {
		rb.Header.Set(HeaderContentType, typ)
	}
	for _, option := range options {
		option(rb)
	}
	rb.Retry = cli.retry
	return rb
}

func (cli *Client) roundTrip(ctx context.Context, req *Request, expectedCode int, expectedCodes ...int) (*Response, error) {
	res, err := cli.transport.RoundTrip(ctx, req)
	if err != nil {
		return nil, err
	}
	if err = checkError(res, expectedCode, expectedCodes...); err != nil {
		return nil, err
	}
	return res, nil
}

func (cli *Client) roundTripper(expectedCode int) roundTripper {
	return func(ctx context.Context, req *Request) (*Response, error) {
		return cli.roundTrip(ctx, req, expectedCode)
	}
}

// PreSignedURL return pre-signed url
//   httpMethod: HTTP method, {
//     PutObject: http.MethodPut
//     GetObject: http.MethodGet
//     HeadObject: http.MethodHead
//     DeleteObject: http.MethodDelete
//   },
//   bucket: the bucket name
//   objectKey: the object name
//   ttl: the time-to-live of signed URL
//   options: WithVersionID the version id of the object
//  Deprecated: use PreSignedURL of ClientV2 instead
func (cli *Client) PreSignedURL(httpMethod string, bucket, objectKey string, ttl time.Duration, options ...Option) (string, error) {
	if err := isValidNames(bucket, objectKey); err != nil {
		return "", err
	}
	return cli.newBuilder(bucket, objectKey, options...).
		PreSignedURL(httpMethod, ttl)
}

// PreSignedURL return pre-signed url
func (cli *ClientV2) PreSignedURL(input *PreSignedURLInput) (*PreSignedURLOutput, error) {
	if err := IsValidBucketName(input.Bucket); err != nil {
		return nil, err
	}
	rb := cli.newBuilder(input.Bucket, input.Key)
	for k, v := range input.Header {
		rb.WithHeader(k, v)
	}
	for k, v := range input.Query {
		rb.WithQuery(k, v)
	}
	if input.Expires == 0 {
		input.Expires = 3600
	}
	signedURL, err := rb.PreSignedURL(string(input.HTTPMethod), time.Second*time.Duration(3600))
	if err != nil {
		return nil, err
	}
	signed := make(map[string]string)
	for k := range rb.Header {
		signed[k] = rb.Header.Get(k)
	}
	output := &PreSignedURLOutput{
		SignedUrl:    signedURL,
		SignedHeader: signed,
	}
	return output, nil
}
