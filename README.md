# mfe-vid
Node script to process queued moment videos...

With a valid `refresh_token` you can always get an `access_token` which can be 
then be used to access any Google API. The `refresh_token` does not expire unless the 
client id and client secret oauth2 credentials are updated or revoked. Below is how you get 
the `refresh_token`. It is a manual process that you do only once.

```javascript
var google = require('googleapis');
var oauth2 = google.auth.OAuth2;

//0) Setup oauth2 and variables using oauth2 client ids

var clientId = "MY_CLIENT_ID";
var clientSecret = "MY_CLIENT_SECRET";
var redirectUrl = "MY_REDIRECT_URL";

var oauth2Client = new oauth2(clientId, clientSecret, redirectUrl);

//1) generate a url that asks permissions for YouTube scopes

var scopes = [
  'https://www.googleapis.com/auth/youtube',
  'https://www.googleapis.com/auth/youtube.upload'
];
var url = oauth2Client.generateAuthUrl({
  access_type: 'offline', //offline is what tells the url to generate a refresh_token
  scope: scopes
});

//2) Paste the url generated above into a web browser and approve the application. 
//You will then redirect to the `redirectUrl` with a 'code' query parameter.
//This code is what you use to retrieve the initial tokens, so grab it.

var code = "MY_CODE";

//3) Use the code to retrieve the 'refresh_token' that you will save and use in your 
//services along with the app ids to get access_tokens.

oauth2Client.getToken(code, function(err, tokens) {
  if(!err) {
    console.log(tokens);
  }
});
```