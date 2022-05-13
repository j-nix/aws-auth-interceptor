# aws-auth-interceptor
Intercepts previously authenticated SSO sessions by attaching to google chrome via remote devtools. Returns a list of available accounts/roles for login into AWS.

Attaches to an authenticated session, intercepts the samlResponse and gathers a list of available accounts/roles.

Logs in and configures the AWS CLI with the chosen account - with the persisted session in the browser.
# Usage

Make sure Google Chrome is running with remote devtools enabled
`/Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome --remote-debugging-port=9222 &`

Ensure you are already authenticated with your SSO (e.g - clicking through to the AWS SSO link shows the account login landing page)

The below has been tested with google SSO - others are untested thus far.
`export AWS_LOGIN_URL=https://your.sso.link.here.com`

`go run main.go` (will build soon)
# TODOs
- Possibly hook up with native google login? (or do that in another tool)
- Clean up the way the DOM tree is traversed
- Clean up error handling
- Split into different functions/packages
- Change how the commands are written (aws configure), we can probably bus it
- Add github actions builder
- Tests?
