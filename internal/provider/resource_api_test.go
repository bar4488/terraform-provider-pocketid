//go:build acc
// +build acc

package provider_test

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Trozz/terraform-provider-pocketid/internal/client"
)

func TestAccResourceAPI_basic(t *testing.T) {
	resourceName := "pocketid_api.test"
	rName := acctest.RandomWithPrefix("tf-acc-api")
	uri := "https://" + rName + ".example.com"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "pocketid_api" "test" {
  name     = "Notes"
  resource = %[1]q
  permissions = {
    "notes.read"  = { name = "Read notes", description = "See every note" }
    "notes.write" = { name = "Write notes" }
  }
}
`, uri),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					resource.TestCheckResourceAttr(resourceName, "name", "Notes"),
					resource.TestCheckResourceAttr(resourceName, "resource", uri),
					resource.TestCheckResourceAttr(resourceName, "permissions.%", "2"),
					resource.TestCheckResourceAttr(resourceName, "permissions.notes.read.description", "See every note"),
					resource.TestCheckNoResourceAttr(resourceName, "permissions.notes.write.description"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Rename the API, change a permission's name and add one: same API, in place.
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "pocketid_api" "test" {
  name     = "Notes API"
  resource = %[1]q
  permissions = {
    "notes.read"   = { name = "Read all notes", description = "See every note" }
    "notes.write"  = { name = "Write notes" }
    "notes.delete" = { name = "Delete notes" }
  }
}
`, uri),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", "Notes API"),
					resource.TestCheckResourceAttr(resourceName, "permissions.%", "3"),
					resource.TestCheckResourceAttr(resourceName, "permissions.notes.read.name", "Read all notes"),
				),
			},
			// Remove every permission.
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "pocketid_api" "test" {
  name     = "Notes API"
  resource = %[1]q
}
`, uri),
				Check: resource.TestCheckNoResourceAttr(resourceName, "permissions.%"),
			},
		},
	})
}

func TestAccResourceAPI_invalidResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
resource "pocketid_api" "test" {
  name     = "Bad"
  resource = "not-a-uri"
}
`,
				ExpectError: regexp.MustCompile(`must be an absolute URI`),
			},
		},
	})
}

func TestAccResourceAPIClientGrant_basic(t *testing.T) {
	resourceName := "pocketid_api_client_grant.test"
	rName := acctest.RandomWithPrefix("tf-acc-grant")

	config := func(grant string) string {
		return testAccProviderConfig() + fmt.Sprintf(`
resource "pocketid_api" "test" {
  name     = "Lab"
  resource = "https://%[1]s.example.com"
  permissions = {
    "map.read"  = { name = "Read the map" }
    "map.write" = { name = "Change the map" }
  }
}

resource "pocketid_client" "test" {
  name          = %[1]q
  callback_urls = ["https://%[1]s.example.com/callback"]
}

resource "pocketid_api_client_grant" "test" {
  api_id    = pocketid_api.test.id
  client_id = pocketid_client.test.id
%[2]s
}
`, rName, grant)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(`  user_delegated_permissions = ["map.read"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckGrantID(resourceName),
					resource.TestCheckResourceAttr(resourceName, "user_delegated_permissions.#", "1"),
					resource.TestCheckTypeSetElemAttr(resourceName, "user_delegated_permissions.*", "map.read"),
					resource.TestCheckNoResourceAttr(resourceName, "client_permissions.#"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Add client-credentials access next to the user-delegated one.
			{
				Config: config(`  user_delegated_permissions = ["map.read", "map.write"]
  client_permissions         = ["map.read"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "user_delegated_permissions.#", "2"),
					resource.TestCheckResourceAttr(resourceName, "client_permissions.#", "1"),
					resource.TestCheckTypeSetElemAttr(resourceName, "client_permissions.*", "map.read"),
				),
			},
			// Access without any permission is an empty set, not "no access".
			{
				Config: config(`  client_permissions = []`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(resourceName, "user_delegated_permissions.#"),
					resource.TestCheckResourceAttr(resourceName, "client_permissions.#", "0"),
				),
			},
		},
	})
}

func TestAccResourceAPIClientGrant_unknownPermission(t *testing.T) {
	rName := acctest.RandomWithPrefix("tf-acc-grant")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "pocketid_api" "test" {
  name        = "Lab"
  resource    = "https://%[1]s.example.com"
  permissions = { "map.read" = { name = "Read the map" } }
}

resource "pocketid_client" "test" {
  name          = %[1]q
  callback_urls = ["https://%[1]s.example.com/callback"]
}

resource "pocketid_api_client_grant" "test" {
  api_id                     = pocketid_api.test.id
  client_id                  = pocketid_client.test.id
  user_delegated_permissions = ["map.delete"]
}
`, rName),
				ExpectError: regexp.MustCompile(`The API has no permission "map.delete"`),
			},
		},
	})
}

// Pocket ID silently refuses client access to a public client; the provider
// must say so instead of storing access that does not exist.
func TestAccResourceAPIClientGrant_publicClientNoClientAccess(t *testing.T) {
	rName := acctest.RandomWithPrefix("tf-acc-grant")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "pocketid_api" "test" {
  name        = "Lab"
  resource    = "https://%[1]s.example.com"
  permissions = { "map.read" = { name = "Read the map" } }
}

resource "pocketid_client" "test" {
  name          = %[1]q
  is_public     = true
  callback_urls = ["https://%[1]s.example.com/callback"]
}

resource "pocketid_api_client_grant" "test" {
  api_id             = pocketid_api.test.id
  client_id          = pocketid_client.test.id
  client_permissions = ["map.read"]
}
`, rName),
				ExpectError: regexp.MustCompile(`only confidential clients can use the client credentials grant`),
			},
		},
	})
}

func TestAccResourceAPIClientGrant_requiresAccess(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
resource "pocketid_api_client_grant" "test" {
  api_id    = "api"
  client_id = "client"
}
`,
				ExpectError: regexp.MustCompile(`(?s)At least one of these attributes must be configured`),
			},
		},
	})
}

func testAccCheckGrantID(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("not found: %s", resourceName)
		}
		want := rs.Primary.Attributes["api_id"] + "/" + rs.Primary.Attributes["client_id"]
		if rs.Primary.ID != want {
			return fmt.Errorf("id is %q, want %q", rs.Primary.ID, want)
		}
		return nil
	}
}

// An API or grant deleted outside Terraform is dropped from state and planned
// again, instead of failing the refresh.
func TestAccResourceAPI_disappears(t *testing.T) {
	rName := acctest.RandomWithPrefix("tf-acc-gone")
	var apiID, clientID string
	config := testAccProviderConfig() + fmt.Sprintf(`
resource "pocketid_api" "test" {
  name        = "Gone"
  resource    = "https://%[1]s.example.com"
  permissions = { "x.read" = { name = "Read" } }
}

resource "pocketid_client" "test" {
  name          = %[1]q
  callback_urls = ["https://%[1]s.example.com/callback"]
}

resource "pocketid_api_client_grant" "test" {
  api_id                     = pocketid_api.test.id
  client_id                  = pocketid_client.test.id
  user_delegated_permissions = ["x.read"]
}
`, rName)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrWith("pocketid_api.test", "id", func(v string) error { apiID = v; return nil }),
					resource.TestCheckResourceAttrWith("pocketid_client.test", "id", func(v string) error { clientID = v; return nil }),
				),
			},
			{
				PreConfig:          func() { mustSucceed(t, testAccClient(t).DeleteAPIClientGrant(apiID, clientID)) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: config},
			{
				PreConfig:          func() { mustSucceed(t, testAccClient(t).DeleteAPI(apiID)) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func testAccClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.NewClient(os.Getenv("POCKETID_BASE_URL"), os.Getenv("POCKETID_API_TOKEN"), false, 30)
	mustSucceed(t, err)
	return c
}

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
