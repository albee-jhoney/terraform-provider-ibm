package ibm

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/apache/incubator-openwhisk-client-go/whisk"
	slsession "github.com/softlayer/softlayer-go/session"

	bluemix "github.com/IBM-Bluemix/bluemix-go"
	"github.com/IBM-Bluemix/bluemix-go/api/account/accountv1"
	"github.com/IBM-Bluemix/bluemix-go/api/account/accountv2"
	"github.com/IBM-Bluemix/bluemix-go/api/container/containerv1"
	"github.com/IBM-Bluemix/bluemix-go/api/iampap/iampapv1"
	"github.com/IBM-Bluemix/bluemix-go/api/mccp/mccpv2"
	bxsession "github.com/IBM-Bluemix/bluemix-go/session"
)

//SoftlayerRestEndpoint rest endpoint of SoftLayer
const SoftlayerRestEndpoint = "https://api.softlayer.com/rest/v3"

//BluemixRegion ...
var BluemixRegion string

var (
	errEmptySoftLayerCredentials = errors.New("softlayer_username and softlayer_api_key must be provided. Please see the documentation on how to configure them")
	errEmptyBluemixCredentials   = errors.New("bluemix_api_key must be provided. Please see the documentation on how to configure it")
	errEmptyOpenWhiskCredentials = errors.New("openwhisk_host and openwhisk_auth_key must be provided. Please see the documentation on how to configure them")
)

//Config stores user provider input
type Config struct {
	//BluemixAPIKey is the Bluemix api key
	BluemixAPIKey string
	//Bluemix region
	Region string
	//Bluemix API timeout
	BluemixTimeout time.Duration

	//Softlayer end point url
	SoftLayerEndpointURL string

	//Softlayer API timeout
	SoftLayerTimeout time.Duration

	// Softlayer User Name
	SoftLayerUserName string

	// Softlayer API Key
	SoftLayerAPIKey string

	// OpenWhiskAuthKey ...
	OpenWhiskAuthKey string

	// OpenWhiskHost ...
	OpenWhiskHost string

	//Retry Count for API calls
	//Unexposed in the schema at this point as they are used only during session creation for a few calls
	//When sdk implements it we an expose them for expected behaviour
	//https://github.com/softlayer/softlayer-go/issues/41
	RetryCount int
	//Constant Retry Delay for API calls
	RetryDelay time.Duration
}

//Session stores the information required for communication with the SoftLayer and Bluemix API
type Session struct {
	// SoftLayerSesssion is the the SoftLayer session used to connect to the SoftLayer API
	SoftLayerSession *slsession.Session

	// BluemixSession is the the Bluemix session used to connect to the Bluemix API
	BluemixSession *bxsession.Session
}

// ClientSession ...
type ClientSession interface {
	SoftLayerSession() *slsession.Session
	BluemixSession() (*bxsession.Session, error)
	ContainerAPI() (containerv1.ContainerServiceAPI, error)
	IAMAPI() (iampapv1.IAMPAPAPI, error)
	MccpAPI() (mccpv2.MccpServiceAPI, error)
	BluemixAcccountAPI() (accountv2.AccountServiceAPI, error)
	BluemixAcccountv1API() (accountv1.AccountServiceAPI, error)
	OpenWhiskClient() (*whisk.Client, error)
}

type clientSession struct {
	session *Session

	csConfigErr  error
	csServiceAPI containerv1.ContainerServiceAPI

	cfConfigErr  error
	cfServiceAPI mccpv2.MccpServiceAPI

	iamConfigErr  error
	iamServiceAPI iampapv1.IAMPAPAPI

	accountConfigErr     error
	bmxAccountServiceAPI accountv2.AccountServiceAPI

	accountV1ConfigErr     error
	bmxAccountv1ServiceAPI accountv1.AccountServiceAPI

	wskConfigErr    error
	openWhiskClient *whisk.Client
}

// OpenWhiskClient provides OpenWhisk Client ...
func (sess clientSession) OpenWhiskClient() (*whisk.Client, error) {
	return sess.openWhiskClient, sess.wskConfigErr
}

// SoftLayerSession providers SoftLayer Session
func (sess clientSession) SoftLayerSession() *slsession.Session {
	return sess.session.SoftLayerSession
}

// MccpAPI provides Multi Cloud Controller Proxy APIs ...
func (sess clientSession) MccpAPI() (mccpv2.MccpServiceAPI, error) {
	return sess.cfServiceAPI, sess.cfConfigErr
}

// BluemixAcccountAPI ...
func (sess clientSession) BluemixAcccountAPI() (accountv2.AccountServiceAPI, error) {
	return sess.bmxAccountServiceAPI, sess.accountConfigErr
}

// BluemixAcccountAPI ...
func (sess clientSession) BluemixAcccountv1API() (accountv1.AccountServiceAPI, error) {
	return sess.bmxAccountv1ServiceAPI, sess.accountV1ConfigErr
}

// IAMAPI provides IAM PAP APIs ...
func (sess clientSession) IAMAPI() (iampapv1.IAMPAPAPI, error) {
	return sess.iamServiceAPI, sess.iamConfigErr
}

// ContainerAPI provides Container Service APIs ...
func (sess clientSession) ContainerAPI() (containerv1.ContainerServiceAPI, error) {
	return sess.csServiceAPI, sess.csConfigErr
}

// BluemixSession to provide the Bluemix Session
func (sess clientSession) BluemixSession() (*bxsession.Session, error) {
	return sess.session.BluemixSession, sess.cfConfigErr
}

// ClientSession configures and returns a fully initialized ClientSession
func (c *Config) ClientSession() (interface{}, error) {
	sess, err := newSession(c)
	if err != nil {
		return nil, err
	}
	session := clientSession{
		session: sess,
	}

	if c.OpenWhiskAuthKey == "" || c.OpenWhiskHost == "" {
		session.wskConfigErr = errEmptyOpenWhiskCredentials
	} else {
		if os.Getenv("TF_LOG") != "" {
			whisk.SetDebug(true)
			whisk.SetVerbose(true)
		}
		wskClient, err := whisk.NewClient(http.DefaultClient, &whisk.Config{
			AuthToken: c.OpenWhiskAuthKey,
			Host:      c.OpenWhiskHost,
			Debug:     true,
			Verbose:   true,
		})
		if err != nil {
			session.wskConfigErr = err
		}
		session.openWhiskClient = wskClient
	}

	if sess.BluemixSession == nil {
		//Can be nil only  if bluemix_api_key is not provided
		log.Println("Skipping Bluemix Clients configuration")
		session.csConfigErr = errEmptyBluemixCredentials
		session.cfConfigErr = errEmptyBluemixCredentials
		session.accountConfigErr = errEmptyBluemixCredentials
		session.accountV1ConfigErr = errEmptyBluemixCredentials
		session.iamConfigErr = errEmptyBluemixCredentials
		return session, nil
	}

	BluemixRegion = sess.BluemixSession.Config.Region
	cfAPI, err := mccpv2.New(sess.BluemixSession)
	if err != nil {
		session.cfConfigErr = fmt.Errorf("Error occured while configuring MCCP service: %q", err)
	}
	session.cfServiceAPI = cfAPI

	accAPI, err := accountv2.New(sess.BluemixSession)
	if err != nil {
		session.accountConfigErr = fmt.Errorf("Error occured while configuring  Account Service: %q", err)
	}
	session.bmxAccountServiceAPI = accAPI

	clusterAPI, err := containerv1.New(sess.BluemixSession)
	if err != nil {
		session.csConfigErr = fmt.Errorf("Error occured while configuring Container Service for K8s cluster: %q", err)
	}
	session.csServiceAPI = clusterAPI

	accv1API, err := accountv1.New(sess.BluemixSession)
	if err != nil {
		session.accountV1ConfigErr = fmt.Errorf("Error occured while configuring Bluemix Accountv1 Service: %q", err)
	}
	session.bmxAccountv1ServiceAPI = accv1API

	iampap, err := iampapv1.New(sess.BluemixSession)
	if err != nil {
		session.iamConfigErr = fmt.Errorf("Error occured while configuring Bluemix IAMPAP Service: %q", err)
	}
	session.iamServiceAPI = iampap
	return session, nil
}

func newSession(c *Config) (*Session, error) {
	ibmSession := &Session{}

	log.Println("Configuring SoftLayer Session ")
	softlayerSession := &slsession.Session{
		Endpoint: c.SoftLayerEndpointURL,
		Timeout:  c.SoftLayerTimeout,
		UserName: c.SoftLayerUserName,
		APIKey:   c.SoftLayerAPIKey,
		Debug:    os.Getenv("TF_LOG") != "",
	}
	ibmSession.SoftLayerSession = softlayerSession

	if c.BluemixAPIKey != "" {
		log.Println("Configuring Bluemix Session")
		var sess *bxsession.Session
		bmxConfig := &bluemix.Config{
			BluemixAPIKey: c.BluemixAPIKey,
			Debug:         os.Getenv("TF_LOG") != "",
			HTTPTimeout:   c.BluemixTimeout,
			Region:        c.Region,
			RetryDelay:    &c.RetryDelay,
			MaxRetries:    &c.RetryCount,
		}
		sess, err := bxsession.New(bmxConfig)
		if err != nil {
			return nil, err
		}
		ibmSession.BluemixSession = sess
	}

	return ibmSession, nil
}
