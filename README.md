# aws-auth-interceptor
Intercepts previously logged in AWS sessions by attaching to google chrome. Returns a list of available accounts/roles for login.

Attaches to an authenticated session, intercepts the samlResponse and gathers a list of available accounts/roles.

# TODOs
- Possibly hook up with native google login? (or do that in another tool)
- Clean up the way the DOM tree is traversed
