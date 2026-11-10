# An API (resource server). Access tokens issued for it carry its resource as
# the audience ("aud") and the granted permission keys as scopes.
resource "pocketid_api" "notes" {
  name     = "Notes"
  resource = "https://notes.example.com"

  # Keyed by permission key, i.e. the OAuth scope clients request. Renaming a
  # permission keeps its grants; removing a key removes it from every grant.
  permissions = {
    "notes.read" = {
      name        = "Read notes"
      description = "See every note"
    }
    "notes.write" = {
      name = "Write notes"
    }
  }
}
