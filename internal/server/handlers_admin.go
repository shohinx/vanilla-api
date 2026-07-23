package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/shohinx/vanilla-api/internal/sdk/middleware"
	"github.com/shohinx/vanilla-api/internal/sdk/models"
	"github.com/shohinx/vanilla-api/internal/sdk/sqldb"
)

func (s *Server) ListOrganizations(c *gin.Context) {
	principal, err := middleware.Principal(c)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "unauthorized", nil)
		return
	}
	organizations, err := s.db.ListOrganizations(c.Request.Context(), adminScope(principal))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_list_organizations_error", nil)
		return
	}
	c.JSON(http.StatusOK, organizations)
}

func (s *Server) CreateOrganization(c *gin.Context) {
	principal, err := middleware.Principal(c)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "unauthorized", nil)
		return
	}
	var input models.OrganizationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.TrimSpace(input.Slug)
	organization, err := s.db.CreateOrganization(c.Request.Context(), input, models.MutationContext{
		ActorID:   principal.ID,
		RequestID: c.GetString("request_id"),
	})
	if err != nil {
		switch {
		case errors.Is(err, sqldb.ErrInvalidInput):
			writeError(c, http.StatusBadRequest, "invalid_organization", nil)
		case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
			writeError(c, http.StatusConflict, "organization_already_exists", nil)
		default:
			writeError(c, http.StatusInternalServerError, "internal_create_organization_error", nil)
		}
		return
	}
	c.JSON(http.StatusCreated, organization)
}

func (s *Server) GetOrganization(c *gin.Context) {
	principal, err := middleware.Principal(c)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "unauthorized", nil)
		return
	}
	organization, err := s.db.GetOrganization(
		c.Request.Context(),
		c.Param("organizationID"),
		adminScope(principal),
	)
	if err != nil {
		writeOrganizationMutationError(c, err, "internal_get_organization_error")
		return
	}
	c.JSON(http.StatusOK, organization)
}

func (s *Server) UpdateOrganization(c *gin.Context) {
	principal, err := middleware.Principal(c)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "unauthorized", nil)
		return
	}
	var input models.OrganizationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.TrimSpace(input.Slug)
	if input.Name == "" || input.Slug == "" {
		writeError(c, http.StatusBadRequest, "invalid_organization", nil)
		return
	}
	if scopedID := adminScope(principal).OrganizationID; scopedID != "" && scopedID != c.Param("organizationID") {
		writeError(c, http.StatusForbidden, "forbidden", nil)
		return
	}
	organization, err := s.db.UpdateOrganization(
		c.Request.Context(),
		c.Param("organizationID"),
		input,
		mutationContext(c),
	)
	if err != nil {
		writeOrganizationMutationError(c, err, "internal_update_organization_error")
		return
	}
	c.JSON(http.StatusOK, organization)
}

func writeOrganizationMutationError(c *gin.Context, err error, internalCode string) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "organization_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
		writeError(c, http.StatusConflict, "organization_already_exists", nil)
	case errors.Is(err, sqldb.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, "invalid_organization", nil)
	default:
		writeError(c, http.StatusInternalServerError, internalCode, nil)
	}
}

func adminScope(principal models.Principal) models.AdminScope {
	if principal.Role == models.RoleOwner {
		return models.AdminScope{}
	}
	return models.AdminScope{OrganizationID: principal.OrganizationID}
}

func (s *Server) ResolveMembership(ctx context.Context, subject, organizationID string) (models.Membership, error) {
	return s.db.ResolveMembership(ctx, subject, organizationID)
}

func (s *Server) RestaurantBelongsToOrganization(ctx context.Context, restaurantID, organizationID string) (bool, error) {
	return s.db.RestaurantBelongsToOrganization(ctx, restaurantID, organizationID)
}

func (s *Server) ListOrganizationMemberships(c *gin.Context) {
	memberships, err := s.db.ListMemberships(c.Request.Context(), c.Param("organizationID"))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_list_memberships_error", nil)
		return
	}
	c.JSON(http.StatusOK, memberships)
}

func (s *Server) CreateOrganizationMembership(c *gin.Context) {
	var input models.MembershipInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	membership, err := s.db.CreateMembership(
		c.Request.Context(),
		c.Param("organizationID"),
		input,
		mutationContext(c),
	)
	if err != nil {
		writeMembershipMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, membership)
}

func (s *Server) UpdateOrganizationMembership(c *gin.Context) {
	var input models.MembershipInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	membership, err := s.db.UpdateMembership(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("userID"),
		input,
		mutationContext(c),
	)
	if err != nil {
		writeMembershipMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, membership)
}

func (s *Server) DeleteOrganizationMembership(c *gin.Context) {
	err := s.db.DeleteMembership(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("userID"),
		mutationContext(c),
	)
	if err != nil {
		writeMembershipMutationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) ListAllergens(c *gin.Context) {
	allergens, err := s.db.ListAllergens(c.Request.Context(), c.Param("organizationID"))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_list_allergens_error", nil)
		return
	}
	c.JSON(http.StatusOK, allergens)
}

func (s *Server) CreateAllergen(c *gin.Context) {
	var input models.AllergenInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	allergen, err := s.db.CreateAllergen(
		c.Request.Context(),
		c.Param("organizationID"),
		input,
		mutationContext(c),
	)
	if err != nil {
		writeAllergenMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, allergen)
}

func (s *Server) GetAllergen(c *gin.Context) {
	allergen, err := s.db.GetAllergen(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("allergenID"),
	)
	if err != nil {
		writeAllergenMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, allergen)
}

func (s *Server) UpdateAllergen(c *gin.Context) {
	var input models.AllergenInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	allergen, err := s.db.UpdateAllergen(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("allergenID"),
		input,
		mutationContext(c),
	)
	if err != nil {
		writeAllergenMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, allergen)
}

func (s *Server) DeleteAllergen(c *gin.Context) {
	err := s.db.DeleteAllergen(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("allergenID"),
		mutationContext(c),
	)
	if err != nil {
		writeAllergenMutationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) ListRestaurants(c *gin.Context) {
	restaurants, err := s.db.ListRestaurants(c.Request.Context(), c.Param("organizationID"))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_list_restaurants_error", nil)
		return
	}
	c.JSON(http.StatusOK, restaurants)
}

func (s *Server) CreateRestaurant(c *gin.Context) {
	var input models.RestaurantInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	restaurant, err := s.db.CreateRestaurant(
		c.Request.Context(),
		c.Param("organizationID"),
		input,
		mutationContext(c),
	)
	if err != nil {
		writeRestaurantMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, restaurant)
}

func (s *Server) GetRestaurant(c *gin.Context) {
	restaurant, err := s.db.GetRestaurant(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("restaurantID"),
	)
	if err != nil {
		writeRestaurantMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, restaurant)
}

func (s *Server) UpdateRestaurant(c *gin.Context) {
	var input models.RestaurantInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	restaurant, err := s.db.UpdateRestaurant(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("restaurantID"),
		input,
		mutationContext(c),
	)
	if err != nil {
		writeRestaurantMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, restaurant)
}

func (s *Server) DeleteRestaurant(c *gin.Context) {
	err := s.db.DeleteRestaurant(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("restaurantID"),
		mutationContext(c),
	)
	if err != nil {
		writeRestaurantMutationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) ListBusinessHours(c *gin.Context) {
	businessHours, err := s.db.ListBusinessHours(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("restaurantID"),
	)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_list_business_hours_error", nil)
		return
	}
	c.JSON(http.StatusOK, businessHours)
}

func (s *Server) CreateBusinessHour(c *gin.Context) {
	var input models.BusinessHourInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	businessHour, err := s.db.CreateBusinessHour(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("restaurantID"),
		input,
		mutationContext(c),
	)
	if err != nil {
		writeBusinessHourMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, businessHour)
}

func (s *Server) GetBusinessHour(c *gin.Context) {
	businessHour, err := s.db.GetBusinessHour(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("restaurantID"),
		c.Param("businessHourID"),
	)
	if err != nil {
		writeBusinessHourMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, businessHour)
}

func (s *Server) UpdateBusinessHour(c *gin.Context) {
	var input models.BusinessHourInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	businessHour, err := s.db.UpdateBusinessHour(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("restaurantID"),
		c.Param("businessHourID"),
		input,
		mutationContext(c),
	)
	if err != nil {
		writeBusinessHourMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, businessHour)
}

func (s *Server) DeleteBusinessHour(c *gin.Context) {
	err := s.db.DeleteBusinessHour(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("restaurantID"),
		c.Param("businessHourID"),
		mutationContext(c),
	)
	if err != nil {
		writeBusinessHourMutationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) ListSpecialHours(c *gin.Context) {
	specialHours, err := s.db.ListSpecialHours(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("restaurantID"),
	)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_list_special_hours_error", nil)
		return
	}
	c.JSON(http.StatusOK, specialHours)
}

func (s *Server) CreateSpecialHour(c *gin.Context) {
	var input models.SpecialHourInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	specialHour, err := s.db.CreateSpecialHour(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("restaurantID"),
		input,
		mutationContext(c),
	)
	if err != nil {
		writeSpecialHourMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, specialHour)
}

func (s *Server) GetSpecialHour(c *gin.Context) {
	specialHour, err := s.db.GetSpecialHour(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("restaurantID"),
		c.Param("specialHourID"),
	)
	if err != nil {
		writeSpecialHourMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, specialHour)
}

func (s *Server) UpdateSpecialHour(c *gin.Context) {
	var input models.SpecialHourInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	specialHour, err := s.db.UpdateSpecialHour(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("restaurantID"),
		c.Param("specialHourID"),
		input,
		mutationContext(c),
	)
	if err != nil {
		writeSpecialHourMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, specialHour)
}

func (s *Server) DeleteSpecialHour(c *gin.Context) {
	err := s.db.DeleteSpecialHour(
		c.Request.Context(),
		c.Param("organizationID"),
		c.Param("restaurantID"),
		c.Param("specialHourID"),
		mutationContext(c),
	)
	if err != nil {
		writeSpecialHourMutationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) ListCatalogItems(c *gin.Context) {
	items, err := s.db.ListCatalogItems(c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_list_catalog_items_error", nil)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) CreateCatalogItem(c *gin.Context) {
	var input models.CatalogItemInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	item, err := s.db.CreateCatalogItem(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"), input, mutationContext(c),
	)
	if err != nil {
		writeCatalogItemMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (s *Server) GetCatalogItem(c *gin.Context) {
	item, err := s.db.GetCatalogItem(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"), c.Param("catalogItemID"),
	)
	if err != nil {
		writeCatalogItemMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) UpdateCatalogItem(c *gin.Context) {
	var input models.CatalogItemInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	item, err := s.db.UpdateCatalogItem(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("catalogItemID"), input, mutationContext(c),
	)
	if err != nil {
		writeCatalogItemMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) DeleteCatalogItem(c *gin.Context) {
	err := s.db.DeleteCatalogItem(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("catalogItemID"), mutationContext(c),
	)
	if err != nil {
		writeCatalogItemMutationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) ListIngredients(c *gin.Context) {
	ingredients, err := s.db.ListIngredients(c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_list_ingredients_error", nil)
		return
	}
	c.JSON(http.StatusOK, ingredients)
}

func (s *Server) CreateIngredient(c *gin.Context) {
	var input models.IngredientInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	ingredient, err := s.db.CreateIngredient(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"), input, mutationContext(c),
	)
	if err != nil {
		writeIngredientMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, ingredient)
}

func (s *Server) GetIngredient(c *gin.Context) {
	ingredient, err := s.db.GetIngredient(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"), c.Param("ingredientID"),
	)
	if err != nil {
		writeIngredientMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, ingredient)
}

func (s *Server) UpdateIngredient(c *gin.Context) {
	var input models.IngredientInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	ingredient, err := s.db.UpdateIngredient(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("ingredientID"), input, mutationContext(c),
	)
	if err != nil {
		writeIngredientMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, ingredient)
}

func (s *Server) DeleteIngredient(c *gin.Context) {
	err := s.db.DeleteIngredient(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("ingredientID"), mutationContext(c),
	)
	if err != nil {
		writeIngredientMutationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) ListMenus(c *gin.Context) {
	menus, err := s.db.ListMenus(c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_list_menus_error", nil)
		return
	}
	c.JSON(http.StatusOK, menus)
}

func (s *Server) CreateMenu(c *gin.Context) {
	var input models.MenuInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	menu, err := s.db.CreateMenu(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"), input, mutationContext(c),
	)
	if err != nil {
		writeMenuMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, menu)
}

func (s *Server) GetMenu(c *gin.Context) {
	menu, err := s.db.GetMenu(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"), c.Param("menuID"),
	)
	if err != nil {
		writeMenuMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, menu)
}

func (s *Server) UpdateMenu(c *gin.Context) {
	var input models.MenuInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	menu, err := s.db.UpdateMenu(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("menuID"), input, mutationContext(c),
	)
	if err != nil {
		writeMenuMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, menu)
}

func (s *Server) DeleteMenu(c *gin.Context) {
	err := s.db.DeleteMenu(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("menuID"), mutationContext(c),
	)
	if err != nil {
		writeMenuMutationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) ListMenuSchedules(c *gin.Context) {
	schedules, err := s.db.ListMenuSchedules(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"), c.Param("menuID"),
	)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_list_menu_schedules_error", nil)
		return
	}
	c.JSON(http.StatusOK, schedules)
}

func (s *Server) CreateMenuSchedule(c *gin.Context) {
	var input models.MenuScheduleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	schedule, err := s.db.CreateMenuSchedule(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"), c.Param("menuID"),
		input, mutationContext(c),
	)
	if err != nil {
		writeMenuScheduleMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, schedule)
}

func (s *Server) GetMenuSchedule(c *gin.Context) {
	schedule, err := s.db.GetMenuSchedule(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"), c.Param("menuScheduleID"),
	)
	if err != nil {
		writeMenuScheduleMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, schedule)
}

func (s *Server) UpdateMenuSchedule(c *gin.Context) {
	var input models.MenuScheduleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	schedule, err := s.db.UpdateMenuSchedule(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("menuScheduleID"), input, mutationContext(c),
	)
	if err != nil {
		writeMenuScheduleMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, schedule)
}

func (s *Server) DeleteMenuSchedule(c *gin.Context) {
	err := s.db.DeleteMenuSchedule(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("menuScheduleID"), mutationContext(c),
	)
	if err != nil {
		writeMenuScheduleMutationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) ListMenuSections(c *gin.Context) {
	sections, err := s.db.ListMenuSections(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"), c.Param("menuID"),
	)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_list_menu_sections_error", nil)
		return
	}
	c.JSON(http.StatusOK, sections)
}

func (s *Server) CreateMenuSection(c *gin.Context) {
	var input models.MenuSectionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	section, err := s.db.CreateMenuSection(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"), c.Param("menuID"),
		input, mutationContext(c),
	)
	if err != nil {
		writeMenuSectionMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, section)
}

func (s *Server) GetMenuSection(c *gin.Context) {
	section, err := s.db.GetMenuSection(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("menuID"), c.Param("menuSectionID"),
	)
	if err != nil {
		writeMenuSectionMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, section)
}

func (s *Server) UpdateMenuSection(c *gin.Context) {
	var input models.MenuSectionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	section, err := s.db.UpdateMenuSection(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("menuID"), c.Param("menuSectionID"), input, mutationContext(c),
	)
	if err != nil {
		writeMenuSectionMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, section)
}

func (s *Server) DeleteMenuSection(c *gin.Context) {
	err := s.db.DeleteMenuSection(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("menuID"), c.Param("menuSectionID"), mutationContext(c),
	)
	if err != nil {
		writeMenuSectionMutationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) ListMenuEntries(c *gin.Context) {
	entries, err := s.db.ListMenuEntries(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("menuID"), c.Param("menuSectionID"),
	)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_list_menu_entries_error", nil)
		return
	}
	c.JSON(http.StatusOK, entries)
}

func (s *Server) CreateMenuEntry(c *gin.Context) {
	var input models.MenuEntryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	entry, err := s.db.CreateMenuEntry(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("menuID"), c.Param("menuSectionID"), input, mutationContext(c),
	)
	if err != nil {
		writeMenuEntryMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, entry)
}

func (s *Server) GetMenuEntry(c *gin.Context) {
	entry, err := s.db.GetMenuEntry(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("menuID"), c.Param("menuSectionID"), c.Param("menuEntryID"),
	)
	if err != nil {
		writeMenuEntryMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, entry)
}

func (s *Server) UpdateMenuEntry(c *gin.Context) {
	var input models.MenuEntryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	entry, err := s.db.UpdateMenuEntry(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("menuID"), c.Param("menuSectionID"), c.Param("menuEntryID"), input, mutationContext(c),
	)
	if err != nil {
		writeMenuEntryMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, entry)
}

func (s *Server) DeleteMenuEntry(c *gin.Context) {
	err := s.db.DeleteMenuEntry(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("menuID"), c.Param("menuSectionID"), c.Param("menuEntryID"), mutationContext(c),
	)
	if err != nil {
		writeMenuEntryMutationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) ListDailySpecials(c *gin.Context) {
	dailySpecials, err := s.db.ListDailySpecials(c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_list_daily_specials_error", nil)
		return
	}
	c.JSON(http.StatusOK, dailySpecials)
}

func (s *Server) CreateDailySpecial(c *gin.Context) {
	var input models.DailySpecialInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	dailySpecial, err := s.db.CreateDailySpecial(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"), input, mutationContext(c),
	)
	if err != nil {
		writeDailySpecialMutationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, dailySpecial)
}

func (s *Server) GetDailySpecial(c *gin.Context) {
	dailySpecial, err := s.db.GetDailySpecial(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"), c.Param("dailySpecialID"),
	)
	if err != nil {
		writeDailySpecialMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, dailySpecial)
}

func (s *Server) UpdateDailySpecial(c *gin.Context) {
	var input models.DailySpecialInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	dailySpecial, err := s.db.UpdateDailySpecial(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("dailySpecialID"), input, mutationContext(c),
	)
	if err != nil {
		writeDailySpecialMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, dailySpecial)
}

func (s *Server) DeleteDailySpecial(c *gin.Context) {
	err := s.db.DeleteDailySpecial(
		c.Request.Context(), c.Param("organizationID"), c.Param("restaurantID"),
		c.Param("dailySpecialID"), mutationContext(c),
	)
	if err != nil {
		writeDailySpecialMutationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func writeCatalogItemMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "catalog_item_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
		writeError(c, http.StatusConflict, "catalog_item_already_exists", nil)
	case errors.Is(err, sqldb.ErrForeignKeyViolation):
		writeError(c, http.StatusConflict, "catalog_item_in_use", nil)
	case errors.Is(err, sqldb.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, "invalid_catalog_item", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal_catalog_item_error", nil)
	}
}

func writeIngredientMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "ingredient_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
		writeError(c, http.StatusConflict, "ingredient_already_exists", nil)
	case errors.Is(err, sqldb.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, "invalid_ingredient", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal_ingredient_error", nil)
	}
}

func writeMenuMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "menu_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
		writeError(c, http.StatusConflict, "menu_already_exists", nil)
	case errors.Is(err, sqldb.ErrForeignKeyViolation):
		writeError(c, http.StatusConflict, "menu_in_use", nil)
	case errors.Is(err, sqldb.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, "invalid_menu", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal_menu_error", nil)
	}
}

func writeMenuScheduleMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "menu_schedule_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry), errors.Is(err, sqldb.ErrConflict), errors.Is(err, sqldb.ErrForeignKeyViolation):
		writeError(c, http.StatusConflict, "menu_schedule_conflict", nil)
	case errors.Is(err, sqldb.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, "invalid_menu_schedule", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal_menu_schedule_error", nil)
	}
}

func writeMenuSectionMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "menu_section_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
		writeError(c, http.StatusConflict, "menu_section_already_exists", nil)
	case errors.Is(err, sqldb.ErrForeignKeyViolation):
		writeError(c, http.StatusConflict, "menu_section_in_use", nil)
	case errors.Is(err, sqldb.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, "invalid_menu_section", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal_menu_section_error", nil)
	}
}

func writeMenuEntryMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "menu_entry_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
		writeError(c, http.StatusConflict, "menu_entry_already_exists", nil)
	case errors.Is(err, sqldb.ErrForeignKeyViolation):
		writeError(c, http.StatusConflict, "menu_entry_in_use", nil)
	case errors.Is(err, sqldb.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, "invalid_menu_entry", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal_menu_entry_error", nil)
	}
}

func writeDailySpecialMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "daily_special_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
		writeError(c, http.StatusConflict, "daily_special_already_exists", nil)
	case errors.Is(err, sqldb.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, "invalid_daily_special", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal_daily_special_error", nil)
	}
}

func writeBusinessHourMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "business_hour_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
		writeError(c, http.StatusConflict, "business_hour_already_exists", nil)
	case errors.Is(err, sqldb.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, "invalid_business_hour", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal_business_hour_error", nil)
	}
}

func writeSpecialHourMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "special_hour_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
		writeError(c, http.StatusConflict, "special_hour_already_exists", nil)
	case errors.Is(err, sqldb.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, "invalid_special_hour", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal_special_hour_error", nil)
	}
}

func writeAllergenMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "allergen_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
		writeError(c, http.StatusConflict, "allergen_already_exists", nil)
	case errors.Is(err, sqldb.ErrForeignKeyViolation):
		writeError(c, http.StatusConflict, "allergen_in_use", nil)
	case errors.Is(err, sqldb.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, "invalid_allergen", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal_allergen_error", nil)
	}
}

func writeRestaurantMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "restaurant_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
		writeError(c, http.StatusConflict, "restaurant_already_exists", nil)
	case errors.Is(err, sqldb.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, "invalid_restaurant", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal_restaurant_error", nil)
	}
}

func (s *Server) pendingAdminRoute(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "not_implemented"})
}

func mutationContext(c *gin.Context) models.MutationContext {
	principal, _ := middleware.Principal(c)
	return models.MutationContext{ActorID: principal.ID, RequestID: c.GetString("request_id")}
}

func writeMembershipMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sqldb.ErrDBNotFound):
		writeError(c, http.StatusNotFound, "membership_not_found", nil)
	case errors.Is(err, sqldb.ErrDBDuplicatedEntry):
		writeError(c, http.StatusConflict, "membership_already_exists", nil)
	default:
		writeError(c, http.StatusInternalServerError, "internal_membership_error", nil)
	}
}
