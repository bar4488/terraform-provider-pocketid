resource "pocketid_api" "notes" {
  name     = "Notes"
  resource = "https://notes.example.com"
  permissions = {
    "notes.read"  = { name = "Read notes" }
    "notes.write" = { name = "Write notes" }
  }
}

# A mobile app that signs users in (public client, PKCE) and calls the API on
# their behalf.
resource "pocketid_client" "notes_app" {
  name          = "Notes app"
  is_public     = true
  callback_urls = ["com.example.notes:/oauth2redirect"]
}

resource "pocketid_api_client_grant" "notes_app" {
  api_id                     = pocketid_api.notes.id
  client_id                  = pocketid_client.notes_app.id
  user_delegated_permissions = ["notes.read", "notes.write"]
}

# A backend job that calls the API as itself (client credentials).
resource "pocketid_client" "indexer" {
  name          = "Notes indexer"
  callback_urls = ["https://indexer.example.com/callback"]
}

resource "pocketid_api_client_grant" "indexer" {
  api_id             = pocketid_api.notes.id
  client_id          = pocketid_client.indexer.id
  client_permissions = ["notes.read"]
}
