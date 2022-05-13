package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sts"
	"github.com/fatih/color"
	"github.com/mafredri/cdp"
	"github.com/mafredri/cdp/devtool"
	"github.com/mafredri/cdp/protocol/dom"
	"github.com/mafredri/cdp/protocol/page"
	"github.com/mafredri/cdp/rpcc"
	"github.com/manifoldco/promptui"

	"golang.org/x/sync/errgroup"
)

var (
	URL                = os.Getenv("AWS_LOGIN_URL")
	SAML_PROVIDER_NAME = os.Getenv("AWS_SAML_PROVIDER_NAME")
)

func main() {
	// TODO: Check if env var set
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	devt := devtool.New("http://localhost:9222")
	pt, err := devt.Get(ctx, devtool.Page)
	if err != nil {
		return
	}

	// Connect to WebSocket URL (page) that speaks the Chrome DevTools Protocol.
	conn, err := rpcc.DialContext(ctx, pt.WebSocketDebuggerURL)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer conn.Close() // Cleanup.

	// Create a new CDP Client that uses conn.
	c := cdp.NewClient(conn)

	// Give enough capacity to avoid blocking any event listeners
	abort := make(chan error, 2)

	// Watch the abort channel.
	go func() {
		select {
		case <-ctx.Done():
		case err := <-abort:
			fmt.Printf("aborted: %s\n", err.Error())
			cancel()
		}
	}()

	// Setup event handlers early because domain events can be sent as
	// soon as Enable is called on the domain.
	if err = abortOnErrors(ctx, c, abort); err != nil {
		fmt.Println(err)
		return
	}

	if err = runBatch(
		// Enable all the domain events that we're interested in.
		func() error { return c.DOM.Enable(ctx) },
	); err != nil {
		fmt.Println(err)
		return
	}

	domLoadTimeout := 5 * time.Second
	err = navigate(ctx, c.Page, URL, domLoadTimeout)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("Navigated to: %s\n", URL)

	doc, err := c.DOM.GetDocument(ctx, nil)
	if err != nil {
	}

	// Get the outer HTML for the page.
	result, err := c.DOM.GetOuterHTML(ctx, &dom.GetOuterHTMLArgs{
		NodeID: &doc.Root.NodeID,
	})
	if err != nil {
	}

	// fmt.Printf("HTML: %s\n", result.OuterHTML)
	accountsMap := make(map[string]string)
	accountsList := []string{}

	// hacky workaround to get account names, until we can get the div properly!
	for _, line := range strings.Split(strings.TrimSuffix(result.OuterHTML, "\n"), "\n") {
		if strings.Contains(line, "div class=\"saml-account-name\"") {
			name := strings.Fields(strings.TrimSpace(line))[2]
			id := strings.Fields(strings.TrimSpace(line))[3]
			tmp := strings.ReplaceAll(id, "</div>", "")
			tmp2 := strings.ReplaceAll(tmp, "(", "")
			cleanedId := strings.ReplaceAll(tmp2, ")", "")
			accountsMap[name] = cleanedId
			accountsList = append(accountsList, fmt.Sprintf("%s:%s", name, cleanedId))
		}
	}
	fmt.Printf("Accounts map is %#v", accountsMap)

	//Change to just SAML resp
	samlResponseNode, err := c.DOM.QuerySelector(ctx, dom.NewQuerySelectorArgs(doc.Root.NodeID, "input[name=\"SAMLResponse\"]"))
	if err != nil {
		fmt.Println(err)
		return
	}

	samlresp, err := c.DOM.DescribeNode(ctx, &dom.DescribeNodeArgs{
		NodeID: &samlResponseNode.NodeID,
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	var samlResponse string
	if contains(samlresp.Node.Attributes, "SAMLResponse") {
		// hacky clean up
		samlResponse = samlresp.Node.Attributes[len(samlresp.Node.Attributes)-1]
	}

	accountPrompt := promptui.Select{
		Label: "Select Account",
		Items: accountsList,
	}

	_, selectedAccount, err := accountPrompt.Run()
	if err != nil {
		fmt.Printf("Prompt failed %v\n", err)
		return
	}
	selectedAccountName := strings.Split(selectedAccount, ":")[0]
	selectedAccountID := strings.Split(selectedAccount, ":")[1]

	rolePrompt := promptui.Select{
		Label: "Select role (note if you do not have access, this will not authenticate.)",
		// TODO - get this dynamically - there is a WIP of this traversing the DOM in another branch
		// Change the available roles here manually for testing out
		Items: []string{"users/admin-user", "users/developer-user"},
	}
	_, selectedRole, err := rolePrompt.Run()
	if err != nil {
		fmt.Printf("Prompt failed %v\n", err)
		return
	}
	// TODO - make this a bit nicer!!
	color.Yellow("Logging into account %s(%s) with role %s", selectedAccountName, selectedAccountID, selectedRole)
	// generatedArn := fmt.Sprintf("arn:aws:iam::%s:role/%s", selectedAccountID, selectedRole)
	// fmt.Printf("\nDEBUG: Using this arn%s\n", generatedArn)

	if SAML_PROVIDER_NAME == "" {
		SAML_PROVIDER_NAME = "google"
	}
	err = awsLogin(samlResponse, selectedAccountID, selectedRole, SAML_PROVIDER_NAME)
	if err != nil {
		color.Red(fmt.Sprintf("\n✘ Saml provider not found for this account under name \"%s\"", SAML_PROVIDER_NAME))
		color.Red("✘ Trying provider named \"g\" as is sometimes found in legacy accounts...")
		err = awsLogin(samlResponse, selectedAccountID, selectedRole, "g")
		if err != nil {
			color.Red("✘ Login failure! %s", err)
			os.Exit(1)
		}
	}
	color.Green("✔ Logged in!")
	// TODO - also get a list of all roles we can assume from their tags
}

func awsLogin(samlResponse string, accountId, role, providerName string) error {
	svc := sts.New(session.New())
	input := &sts.AssumeRoleWithSAMLInput{
		DurationSeconds: aws.Int64(3600),
		PrincipalArn:    aws.String(fmt.Sprintf("arn:aws:iam::%s:saml-provider/%s", accountId, providerName)),
		RoleArn:         aws.String(fmt.Sprintf("arn:aws:iam::%s:role/%s", accountId, role)),
		SAMLAssertion:   aws.String(samlResponse),
	}

	result, err := svc.AssumeRoleWithSAML(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			default:
				return aerr
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			return err
		}
	}
	cmd := exec.Command("aws", "configure", "set", "aws_access_key_id", *result.Credentials.AccessKeyId)
	_, err = cmd.Output()

	if err != nil {
		fmt.Println(err.Error())
	}

	cmd = exec.Command("aws", "configure", "set", "aws_secret_access_key", *result.Credentials.SecretAccessKey)
	_, err = cmd.Output()

	if err != nil {
		fmt.Println(err.Error())
	}

	cmd = exec.Command("aws", "configure", "set", "aws_session_token", *result.Credentials.SessionToken)
	_, err = cmd.Output()

	if err != nil {
		fmt.Println(err.Error())
	}
	return nil
}

func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
}

func abortOnErrors(ctx context.Context, c *cdp.Client, abort chan<- error) error {
	exceptionThrown, err := c.Runtime.ExceptionThrown(ctx)
	if err != nil {
		return err
	}

	loadingFailed, err := c.Network.LoadingFailed(ctx)
	if err != nil {
		return err
	}

	go func() {
		defer exceptionThrown.Close() // Cleanup.
		defer loadingFailed.Close()
		for {
			select {
			// Check for exceptions so we can abort as soon
			// as one is encountered.
			case <-exceptionThrown.Ready():
				ev, err := exceptionThrown.Recv()
				if err != nil {
					// This could be any one of: stream closed,
					// connection closed, context deadline or
					// unmarshal failed.
					abort <- err
					return
				}

				// Ruh-roh! Let the caller know something went wrong.
				abort <- ev.ExceptionDetails

			// Check for non-canceled resources that failed
			// to load.
			case <-loadingFailed.Ready():
				ev, err := loadingFailed.Recv()
				if err != nil {
					abort <- err
					return
				}

				// For now, most optional fields are pointers
				// and must be checked for nil.
				canceled := ev.Canceled != nil && *ev.Canceled

				if !canceled {
					abort <- fmt.Errorf("request %s failed: %s", ev.RequestID, ev.ErrorText)
				}
			}
		}
	}()
	return nil
}

// navigate to the URL and wait for DOMContentEventFired. An error is
// returned if timeout happens before DOMContentEventFired.
func navigate(ctx context.Context, pageClient cdp.Page, url string, timeout time.Duration) error {
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()

	// Make sure Page events are enabled.
	err := pageClient.Enable(ctx)
	if err != nil {
		return err
	}

	// Open client for DOMContentEventFired to block until DOM has fully loaded.
	domContentEventFired, err := pageClient.DOMContentEventFired(ctx)
	if err != nil {
		return err
	}
	defer domContentEventFired.Close()

	_, err = pageClient.Navigate(ctx, page.NewNavigateArgs(url))
	if err != nil {
		return err
	}

	_, err = domContentEventFired.Recv()
	return err
}

// runBatchFunc is the function signature for runBatch.
type runBatchFunc func() error

// runBatch runs all functions simultaneously and waits until
// execution has completed or an error is encountered.
func runBatch(fn ...runBatchFunc) error {
	eg := errgroup.Group{}
	for _, f := range fn {
		eg.Go(f)
	}
	return eg.Wait()
}
