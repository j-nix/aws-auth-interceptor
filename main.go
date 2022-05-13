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
	"github.com/mafredri/cdp/protocol/runtime"
	"github.com/mafredri/cdp/rpcc"
	"github.com/manifoldco/promptui"

	"golang.org/x/sync/errgroup"
)

// Cookie represents a browser cookie.
type Cookie struct {
	URL   string `json:"url"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// DocumentInfo contains information about the document.
type DocumentInfo struct {
	Title string `json:"title"`
}

var (
	URL = os.Getenv("AWS_LOGIN_URL")
)

func main() {
	cmd := exec.Command("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "--remote-debugging-port=9222")
	// cmd := exec.Command("killall 'Google Chrome' && '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' --remote-debugging-port=9222 &")
	stdout, err := cmd.Output()

	if err != nil {
		fmt.Println(err.Error())
	}

	// Print the output
	fmt.Println(string(stdout))
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

	// TODO - maybe we can use the javascript stuff here to grab something!
	// Parse information from the document by evaluating JavaScript.

	// TODO tomorrow - we can probably create this array with JS, if not try and extract with the below using go
	// don't forget it
	expression := `
				var nodes = document.querySelectorAll(".saml-account-name, .saml-role-description")
				var list = [].slice.call(nodes)
                list.map(function(e) { return e.innerText; }).join(":")
				// list.forEach(item => map1.set(item,item.innerText));
	`
	evalArgs := runtime.NewEvaluateArgs(expression).SetAwaitPromise(true).SetReturnByValue(true)
	eval, err := c.Runtime.Evaluate(ctx, evalArgs)
	if err != nil {
		fmt.Println(err)
		return
	}
	roles := fmt.Sprintf("%s", eval)
	// hacky workaround to get account names, until we can get the div properly!
	for _, line := range strings.Split(roles, ":") {
		if strings.Contains(line, "Account") {
			fmt.Println(line)
		}
	}

	// Fetch the document root node.
	doc, err := c.DOM.GetDocument(ctx, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	// WIP for the dom elements

	// test, err := c.DOM.DescribeNode(ctx, &dom.DescribeNodeArgs{
	// 	NodeID: &divElements.NodeIDs[5],
	// })
	// if err != nil {
	// 	fmt.Println(err)
	// 	return
	// }
	// fmt.Printf("it is %#v", test.Node)
	// fmt.Printf("it is %#v", test.Node.NodeID)

	// for now we can create a list of available roles/accounts
	// better to get it dynamic in the future

	// THIS PART BELOW DOES CREATE A MAP OF ACCOUNTS -> ACCOUNT IDS BUT IT'S HACKY
	// IT'D BE BETTER IF WE CAN USE THE NATIVE DOM ELEMENTS AND GET NESTED ROLES FOR EACH
	// Fetch the document root node. We can pass nil here
	// since this method only takes optional arguments.
	doc, err = c.DOM.GetDocument(ctx, nil)
	if err != nil {
	}

	inputElements, err := c.DOM.QuerySelectorAll(ctx, dom.NewQuerySelectorAllArgs(doc.Root.NodeID, "input"))
	if err != nil {
		fmt.Println(err)
		return
	}

	// divElements, err := c.DOM.QuerySelectorAll(ctx, dom.NewQuerySelectorAllArgs(doc.Root.NodeID, "div"))
	// if err != nil {
	// 	fmt.Println(err)
	// 	return
	// }

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
			// fmt.Println(strings.Fields(strings.TrimSpace(line)))
		}
	}
	// fmt.Printf("Names: \n%s", accountsMap)

	// Now hack and get roles you can assume per account
	// fmt.Printf("SamlResp? %s\n", inputElements.NodeIDs)
	// for i, _ := range divElements.NodeIDs {
	// 	// Show nodes here
	// 	element, err := c.DOM.DescribeNode(ctx, &dom.DescribeNodeArgs{
	// 		NodeID: &divElements.NodeIDs[i],
	// 	})
	// 	if err != nil {
	// 		fmt.Println(err)
	// 		return
	// 	}

	// 	fmt.Printf("%d it is %#v", i, element)
	// }

	samlresp, err := c.DOM.DescribeNode(ctx, &dom.DescribeNodeArgs{
		NodeID: &inputElements.NodeIDs[1],
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	// Get the samlresponse here
	var samlResponse string
	if contains(samlresp.Node.Attributes, "SAMLResponse") {
		// hacky clean up
		samlResponse = samlresp.Node.Attributes[len(samlresp.Node.Attributes)-1]
	}
	// fmt.Printf("SamlResponse is %s", samlResponse)

	// select stuff
	accountPrompt := promptui.Select{
		Label: "Select Account",
		Items: accountsList,
	}

	_, selectedAccount, err := accountPrompt.Run()
	if err != nil {
		fmt.Printf("Prompt failed %v\n", err)
		return
	}

	rolePrompt := promptui.Select{
		Label: "Select role (note if you do not have access, this will not authenticate.)",
		Items: []string{"users/admin-user", "users/developer-user"},
	}
	_, selectedRole, err := rolePrompt.Run()
	if err != nil {
		fmt.Printf("Prompt failed %v\n", err)
		return
	}

	// TODO - make this a bit nicer!!
	color.Yellow("Logging into account %s with role %s", accountsMap[strings.Split(selectedAccount, ":")[0]], selectedRole)
	// generatedArn := fmt.Sprintf("arn:aws:iam::%s:role/%s", accountsMap[strings.Split(selectedAccount, ":")[0]], selectedRole)
	// fmt.Printf("\nDEBUG: Using this arn%s\n", generatedArn)
	err = awsLogin(samlResponse, accountsMap[strings.Split(selectedAccount, ":")[0]], selectedRole, "google")
	if err != nil {
		color.Red("\n✘ Saml provider not found for this account under name \"google\"")
		color.Red("✘ Trying provider named \"g\" as is sometimes found in legacy accounts...")
		err = awsLogin(samlResponse, accountsMap[strings.Split(selectedAccount, ":")[0]], selectedRole, "g")
		if err != nil {
			color.Red("✘ Login failure! %s", err)
			os.Exit(1)
		}
	}
	color.Green("✔ Logged in!")
	// TODO - also get a list of all roles we can assume from their tags
}

func awsLogin(samlResponse string, account, role, providerName string) error {
	svc := sts.New(session.New())
	input := &sts.AssumeRoleWithSAMLInput{
		DurationSeconds: aws.Int64(3600),
		PrincipalArn:    aws.String(fmt.Sprintf("arn:aws:iam::%s:saml-provider/%s", account, providerName)),
		RoleArn:         aws.String(fmt.Sprintf("arn:aws:iam::%s:role/%s", account, role)),
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
		return nil
	}
	// We can write a file here if we really want to - but let's instead use the commands?
	// file, _ := json.MarshalIndent(result, "", " ")

	// f, err := os.Create("/tmp/aws-auth")

	// if err != nil {
	// 	log.Fatal(err)
	// }

	// defer f.Close()
	// _, err2 := f.WriteString(fmt.Sprintf("%s", file))

	// if err2 != nil {
	// 	log.Fatal(err2)
	// }
	// fmt.Printf("Written creds to temp file /tmp/aws-auth\n")
	// Clean up
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
