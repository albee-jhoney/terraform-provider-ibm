package ibm

import (
	"fmt"
	"net/http"
	"net/url"
	"os"

	bluemix "github.com/IBM-Bluemix/bluemix-go"
	"github.com/IBM-Bluemix/bluemix-go/rest"
	"github.com/apache/incubator-openwhisk-client-go/whisk"
	"github.com/terraform-providers/terraform-provider-ibm/version"
)

//AuthResponse ...
type AuthResponse struct {
	Namespaces []struct {
		Name string
		Key  string
	}
}

//AuthError ...
type AuthError struct {
	Error string
}

//AuthPayload ...
type AuthPayload struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

func whiskClient(c *bluemix.Config, namespace string) (*whisk.Client, error) {
	baseEndpoint := fmt.Sprintf("https://openwhisk.ng.bluemix.net")
	if c.Region != "us-south" {
		baseEndpoint = fmt.Sprintf("https://openwhisk.%s.bluemix.net", c.Region)
	}
	//baseEndpoint = "http://localhost:9090/bluemix/v1/authenticate"
	client := rest.Client{
		DefaultHeader: http.Header{
			"User-Agent": []string{fmt.Sprintf("terraform-provider-ibm/%s", version.Version)},
		},
	}

	authPayload := AuthPayload{
		AccessToken:  c.UAAAccessToken[7:len(c.UAAAccessToken)],
		RefreshToken: c.UAARefreshToken[7:len(c.UAARefreshToken)],
	}

	authEndpoint := fmt.Sprintf("%s/bluemix/v1/authenticate", baseEndpoint)
	request := rest.PostRequest(authEndpoint).Body(authPayload)
	var authResp AuthResponse
	var apiErr AuthError
	_, err := client.Do(request, &authResp, &apiErr)
	if err != nil {
		return nil, fmt.Errorf("Couldn't fetch namespace details %v", err)
	}
	allNamespaces := []string{}
	for _, n := range authResp.Namespaces {
		allNamespaces = append(allNamespaces, n.Name)
		if n.Name == namespace {
			u, _ := url.Parse(fmt.Sprintf("%s/api", baseEndpoint))
			if os.Getenv("TF_LOG") != "" {
				whisk.SetDebug(true)
			}
			wskClient, err := whisk.NewClient(http.DefaultClient, &whisk.Config{
				Namespace: namespace,
				AuthToken: n.Key,
				//BaseURL:   u,
				Host: u.Host,
			})
			return wskClient, err
		}
	}
	return nil, fmt.Errorf("Couldn't fine Whisk Auth Key for namespace %s. Available namespaces are %s", namespace, allNamespaces)
}
