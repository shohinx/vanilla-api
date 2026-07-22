package server

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/shohinx/vanilla-api/internal/sdk/middleware"
	"github.com/shohinx/vanilla-api/internal/sdk/sqldb"
)

const maxJSONBodyBytes int64 = 1 << 20 // 1 MB

func (s *Server) RegisterRoutes() http.Handler {
	router := gin.New()

	router.Use(
		middleware.RequestID(),
		middleware.Logger(s.logger),
		middleware.Recovery(s.logger),
		middleware.SecurityHeaders(),
		middleware.CORS(nil, false),
	)

	// Deployments should restrict this operational endpoint at the ingress or
	// service-network layer.
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	router.GET("/restaurants/:restaurantSlug/menu", s.publicMenuHandler)

	// API v1 route group
	v1 := router.Group("/api/v1")
	{
		// Health check routes (public, unlimited — used by load balancers).
		health := v1.Group("/health")
		{
			health.GET("/readiness", s.HandleReadiness)
			health.GET("/liveness", s.HandleLiveness)
		}

		// Auth routes (public).
		auth := v1.Group("/auth")
		{
			auth.POST("/register", middleware.BodyLimit(maxJSONBodyBytes), s.HandleRegister)
			auth.POST("/login", middleware.BodyLimit(maxJSONBodyBytes), s.HandleLogin)
			auth.POST("/refresh", middleware.BodyLimit(maxJSONBodyBytes), s.HandleRefresh)
			auth.POST("/password/forgot", middleware.BodyLimit(maxJSONBodyBytes), s.HandleForgotPassword)
			auth.POST("/password/reset", middleware.BodyLimit(maxJSONBodyBytes), s.HandleResetPassword)
		}

		user := v1.Group("/user")
		user.Use(middleware.BodyLimit(maxJSONBodyBytes))
		user.Use(middleware.Authenticate(s.jwt))
		{
			user.GET("/me", s.HandleMe)
			user.POST("/me/password/change", s.HandlePasswordChange)
			user.POST("/me/username", s.HandleUpdateUsername)
		}

		admin := v1.Group("/admin")
		user.Use(middleware.BodyLimit(maxJSONBodyBytes))
		admin.Use(middleware.Authenticate(s.jwt))
		admin.Use(middleware.AuthorizeAdmin())
		{
			pending := s.pendingAdminRoute

			users := admin.Group("/users")
			{
				users.GET("", s.HandleListUsers)
				users.POST("/:user_id/roles/grant", s.HandleGrantAdminRole)
				users.POST("/:user_id/roles/revoke", s.HandleRevokeAdminRole)
			}

			organizations := admin.Group("/organizations")
			{
				organizations.GET("", s.ListOrganizations)
				organizations.POST("", s.CreateOrganization)

				organization := organizations.Group("/:organizationID")
				organization.Use(middleware.RequireMembership(s))
				{
					organization.GET("", s.GetOrganization)
					organization.PATCH("", s.UpdateOrganization)

					memberships := organization.Group("/memberships")
					{
						memberships.GET("", s.ListOrganizationMemberships)
						memberships.POST("", s.CreateOrganizationMembership)
						memberships.PATCH("/:userID", s.UpdateOrganizationMembership)
						memberships.DELETE("/:userID", s.DeleteOrganizationMembership)
					}

					allergens := organization.Group("/allergens")
					{
						allergens.GET("", s.ListAllergens)
						allergens.POST("", s.CreateAllergen)
						allergens.GET("/:allergenID", s.GetAllergen)
						allergens.PATCH("/:allergenID", s.UpdateAllergen)
						allergens.DELETE("/:allergenID", s.DeleteAllergen)
					}

					restaurants := organization.Group("/restaurants")
					{
						restaurants.GET("", s.ListRestaurants)
						restaurants.POST("", s.CreateRestaurant)

						restaurant := restaurants.Group("/:restaurantID")
						{
							// Restaurant
							restaurant.GET("", s.GetRestaurant)
							restaurant.PATCH("", s.UpdateRestaurant)
							restaurant.DELETE("", s.DeleteRestaurant)

							businessHours := restaurant.Group("/business-hours")
							{
								businessHours.GET("", s.ListBusinessHours)
								businessHours.POST("", s.CreateBusinessHour)
								businessHours.GET("/:businessHourID", s.GetBusinessHour)
								businessHours.PATCH("/:businessHourID", s.UpdateBusinessHour)
								businessHours.DELETE("/:businessHourID", s.DeleteBusinessHour)
							}

							specialHours := restaurant.Group("/special-hours")
							{
								specialHours.GET("", s.ListSpecialHours)
								specialHours.POST("", s.CreateSpecialHour)
								specialHours.GET("/:specialHourID", s.GetSpecialHour)
								specialHours.PATCH("/:specialHourID", s.UpdateSpecialHour)
								specialHours.DELETE("/:specialHourID", s.DeleteSpecialHour)
							}

							mediaAssets := restaurant.Group("/media-assets")
							{
								mediaAssets.GET("", pending)
								mediaAssets.GET("/:mediaAssetID", pending)
								mediaAssets.PATCH("/:mediaAssetID", pending)
								mediaAssets.DELETE("/:mediaAssetID", pending)
								mediaAssets.POST("/upload", pending)
							}

							catalogItems := restaurant.Group("/catalog-items")
							{
								catalogItems.GET("", s.ListCatalogItems)
								catalogItems.POST("", s.CreateCatalogItem)
								catalogItems.GET("/:catalogItemID", s.GetCatalogItem)
								catalogItems.PATCH("/:catalogItemID", s.UpdateCatalogItem)
								catalogItems.DELETE("/:catalogItemID", s.DeleteCatalogItem)
							}

							ingredients := restaurant.Group("/ingredients")
							{
								ingredients.GET("", s.ListIngredients)
								ingredients.POST("", s.CreateIngredient)
								ingredients.GET("/:ingredientID", s.GetIngredient)
								ingredients.PATCH("/:ingredientID", s.UpdateIngredient)
								ingredients.DELETE("/:ingredientID", s.DeleteIngredient)
							}

							// Menus

							menus := restaurant.Group("/menus")
							{
								// --- Base Menu Routes ---
								menus.GET("", s.ListMenus)
								menus.POST("", s.CreateMenu)
								menus.GET("/qr", s.GetMenuQR) // Placed before parametric routes to avoid route collision issues
								menus.GET("/:menuID", s.GetMenu)
								menus.PATCH("/:menuID", s.UpdateMenu)
								menus.DELETE("/:menuID", s.DeleteMenu)

								// --- Menu Schedules ---
								schedules := menus.Group("/:menuID/schedules")
								{
									schedules.GET("", s.ListMenuSchedules)
									schedules.POST("", s.CreateMenuSchedule)
									schedules.GET("/:menuScheduleID", s.GetMenuSchedule)
									schedules.PATCH("/:menuScheduleID", s.UpdateMenuSchedule)
									schedules.DELETE("/:menuScheduleID", s.DeleteMenuSchedule)
								}

								// --- Menu Sections ---
								sections := menus.Group("/:menuID/sections")
								{
									sections.GET("", s.ListMenuSections)
									sections.POST("", s.CreateMenuSection)
									sections.GET("/:menuSectionID", s.GetMenuSection)
									sections.PATCH("/:menuSectionID", s.UpdateMenuSection)
									sections.DELETE("/:menuSectionID", s.DeleteMenuSection)

									// --- Menu Section Entries ---
									entries := sections.Group("/:menuSectionID/entries")
									{
										entries.GET("", s.ListMenuEntries)
										entries.POST("", s.CreateMenuEntry)
										entries.GET("/:menuEntryID", s.GetMenuEntry)
										entries.PATCH("/:menuEntryID", s.UpdateMenuEntry)
										entries.DELETE("/:menuEntryID", s.DeleteMenuEntry)
									}
								}
							}

							dailySpecials := restaurant.Group("/daily-specials")
							{
								dailySpecials.GET("", s.ListDailySpecials)
								dailySpecials.POST("", s.CreateDailySpecial)
								dailySpecials.GET("/:dailySpecialID", s.GetDailySpecial)
								dailySpecials.PATCH("/:dailySpecialID", s.UpdateDailySpecial)
								dailySpecials.DELETE("/:dailySpecialID", s.DeleteDailySpecial)
							}

							diningTables := restaurant.Group("/dining-tables")
							{
								diningTables.GET("", pending)
								diningTables.POST("", pending)
								diningTables.GET("/:diningTableID", pending)
								diningTables.PATCH("/:diningTableID", pending)
								diningTables.DELETE("/:diningTableID", pending)
							}
						}
					}
				}
			}
		}
	}

	return router
}

func (s *Server) publicMenuHandler(c *gin.Context) {
	snapshot, err := s.repository.PublicMenuSnapshot(
		c.Request.Context(),
		c.Param("restaurantSlug"),
	)
	if errors.Is(err, sqldb.ErrDBNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "menu_not_found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "menu_unavailable"})
		return
	}
	etag := `"` + snapshot.ETag + `"`
	c.Header("ETag", etag)
	c.Header("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	c.Header("Last-Modified", snapshot.GeneratedAt.UTC().Format(http.TimeFormat))
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", snapshot.Payload)
}
