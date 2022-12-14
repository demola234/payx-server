package controllers

import (
	"context"
	"go-service/payx/database"
	"strings"

	"go-service/payx/helpers"
	"go-service/payx/models"

	"log"
	"net/http"
	"strconv"

	"time"

	"github.com/cloudinary/cloudinary-go"
	"github.com/cloudinary/cloudinary-go/api/uploader"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

var validate = validator.New()
var userCollection *mongo.Collection = database.PayxCollection(database.Client, "Users")

func GetUsers() gin.HandlerFunc {
	return func(c *gin.Context) {
		var ctx, cancel = context.WithTimeout(context.Background(), 100*time.Second)

		recordPerPage, err := strconv.Atoi(c.Query("recordPerPage"))
		if err != nil || recordPerPage < 1 {
			recordPerPage = 10
		}

		page, err1 := strconv.Atoi(c.Query("page"))
		if err1 != nil || page < 1 {
			page = 1
		}

		startIndex := (page - 1) * recordPerPage
		startIndex, err = strconv.Atoi(c.Query("startIndex"))

		matchStage := bson.D{{"$match", bson.D{{}}}}

		groupStage := bson.D{{"$group", bson.D{{"_id", bson.D{{"_id", "null"}}}, {"total_count", bson.D{{"$sum", 1}}}, {"data", bson.D{{"$push", "$$ROOT"}}}}}}

		projectStage := bson.D{
			{"$project", bson.D{
				{"_id", 0},
				{"total_count", 1},
				{"users", bson.D{{"$slice", []interface{}{"$data", startIndex, recordPerPage}}}},
			}}}
		unStage := bson.D{{"$unset", "password"}}

		result, err := userCollection.Aggregate(ctx, mongo.Pipeline{matchStage, unStage, groupStage, projectStage})
		defer cancel()
		if err != nil {
			c.JSON(500, gin.H{"status": "Failure",
				"message": "An error Occurred while listing user items"})

		}
		var allUsers []bson.M
		if err = result.All(ctx, &allUsers); err != nil {
			log.Fatal(err)
		}

		c.JSON(200, gin.H{"status": "Success", "data": allUsers[0]})

	}
}

func GetUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		var ctx, cancel = context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		userId := c.Param("user_id")
		var user models.User

		err := userCollection.FindOne(ctx, bson.M{"user_id": userId}).Decode(&user)
		if err != nil {
			c.JSON(500, gin.H{"error": "Could not get the user"})
		}

		c.JSON(200, user)
	}
}

func SignUp() gin.HandlerFunc {
	return func(c *gin.Context) {
		var ctx, cancel = context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		var users models.User
		//convert the JSON data coming from postman to something that golang understands
		err := c.BindJSON(&users)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": err.Error(),
			})
		}
		//validate the data based on user struct
		validationErr := validate.Struct(users)
		if validationErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": validationErr.Error()})
			return
		}
		//you'll check if the email has already been used by another user
		count1, err := userCollection.CountDocuments(ctx, bson.M{"email": users.Email})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if count1 > 0 {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "this email  already exits"})
			return
		}

		//you'll also check if the phone no. has already been used by another user
		count2, err := userCollection.CountDocuments(ctx, bson.M{"phone": users.Phone})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if count2 > 0 {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "this phone number  already exits"})
			return
		}

		//hash password
		password := HashPassword(*users.Password)
		users.Password = &password

		//create some extra details for the user object - created_at, updated_at, ID
		users.Created_at, _ = time.Parse(time.RFC3339, time.Now().Format(time.RFC3339))
		users.Updated_at, _ = time.Parse(time.RFC3339, time.Now().Format(time.RFC3339))
		users.ID = primitive.NewObjectID()
		users.User_id = users.ID.Hex()

		//Create User Account
		accountDetails, error := CreateAccountDetails(c)
		if error != nil {
			c.IndentedJSON(http.StatusInternalServerError, gin.H{
				"error": "Could not create Account number",
			})
		}
		users.Account_Number = &accountDetails.Account_Number
		users.Account_id = &accountDetails.Account_Id
		users.Balance = &accountDetails.Account_Balance

		//generate token and refresh token (generate all tokens function from helper)
		token, refreshToken, _ := helpers.GenerateAllTokens(*users.Email, *users.First_name, *users.Last_name, users.User_id, *users.Account_Number)
		users.Token = &token
		users.Refresh_Token = &refreshToken

		cardDetails, error := CreateUsersCard(c)
		if error != nil {
			c.IndentedJSON(http.StatusInternalServerError, gin.H{
				"error": "Could not create Account number",
			})
		}

		users.Card_id = &cardDetails.Card_ID

		//if all ok, then you insert this new user into the user collection
		_, err = userCollection.InsertOne(ctx, users)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": err.Error(),
			})
		}
		//return status OK and send the result back
		c.IndentedJSON(http.StatusCreated, users)
	}
}

func Login() gin.HandlerFunc {
	return func(c *gin.Context) {
		var ctx, cancel = context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		var users models.User
		var foundUsers models.User
		//convert the login data from postman which is in JSON to golang readable format
		if err := c.BindJSON(&users); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "An Error Occurred",
			})
			return
		}
		//find a user with that email and see if that user even exists
		err := userCollection.FindOne(ctx, bson.M{"email": users.Email}).Decode(&foundUsers)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "User Already exists",
			})
			return
		}
		//then you will verify the password
		msg, isPasswordValid := VerifyPassword(*users.Password, *foundUsers.Password)
		if !isPasswordValid {
			c.JSON(http.StatusInternalServerError, gin.H{"error": msg})
			return
		}
		//if all goes well, then you'll generate tokens
		token, refreshToken, _ := helpers.GenerateAllTokens(*foundUsers.Email, *foundUsers.First_name, *foundUsers.Last_name, foundUsers.User_id, *foundUsers.Account_Number)

		//update tokens - token and refresh token
		helpers.UpdateAllTokens(token, refreshToken, foundUsers.User_id)
		//return statusOK
		c.JSON(http.StatusOK, foundUsers)
	}
}

func UploadProfileImage() gin.HandlerFunc {
	return func(c *gin.Context) {
		var ctx, cancel = context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		var user models.User
		// Create our instance
		cld, _ := cloudinary.NewFromURL("cloudinary://211576879732455:W6p_HMMIrDZkEfheHRUHIkSTdOo@dcnuiaskr")
		// Get the preferred name of the file if its not supplied
		fileName := "profileImage"

		image, _, err := c.Request.FormFile("image")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   err,
				"message": "Failed to upload",
			})
		}

		result, err := cld.Upload.Upload(c, image, uploader.UploadParams{
			PublicID: fileName,
			// Split the tags by comma
			Tags: strings.Split(",", "profile"),
		})

		if err != nil {
			c.String(http.StatusConflict, "Upload to cloudinary failed")
		}

		userId := c.PostForm("user_id")
		filter := bson.M{"user_id": userId}

		var updateObj primitive.D

		user.Image = &result.URL

		if *user.Image != "" {
			updateObj = append(updateObj, bson.E{"image", user.Image})
		}

		user.Updated_at, _ = time.Parse(time.RFC3339, time.Now().Format(time.RFC3339))
		updateObj = append(updateObj, bson.E{"updated_at", user.Updated_at})

		upsert := true
		opt := options.UpdateOptions{
			Upsert: &upsert,
		}

		_, err = userCollection.UpdateOne(
			ctx,
			filter,
			bson.D{{"$set", updateObj}},
			&opt,
		)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Unable to Update"})

		}

		// c.JSON(http.StatusOK, gin.H{"message": "User Updated Successfully!"})

		c.JSON(http.StatusCreated, gin.H{
			"message":   "Successfully uploaded the file",
			"secureURL": result.SecureURL,
			"publicURL": result.URL,
			"image":     user.Image,
		})
	}

}

func UpdateUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		var ctx, cancel = context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		var user models.User

		if err := c.BindJSON(&user); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		userId := c.Param("user_id")
		filter := bson.M{"user_id": userId}

		var updateObj primitive.D

		if user.First_name != nil {
			updateObj = append(updateObj, bson.E{"first_name", user.First_name})
		}

		if user.Last_name != nil {
			updateObj = append(updateObj, bson.E{"last_name", user.Last_name})
		}

		if user.Phone != nil {
			updateObj = append(updateObj, bson.E{"phone", user.Phone})
		}

		user.Updated_at, _ = time.Parse(time.RFC3339, time.Now().Format(time.RFC3339))
		updateObj = append(updateObj, bson.E{"updated_at", user.Updated_at})

		upsert := true
		opt := options.UpdateOptions{
			Upsert: &upsert,
		}
		_, err := userCollection.UpdateOne(
			ctx,
			filter,
			bson.D{{"$set", updateObj}},
			&opt,
		)
		if err != nil {
			// msg := "User Failed"
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}

		c.JSON(http.StatusOK, gin.H{"message": "User Updated Successfully!"})
	}
}

func HashPassword(password string) string {
	// Hashing the users password with bcrypt
	bytes, error := bcrypt.GenerateFromPassword([]byte(password), 15)
	if error != nil {
		log.Panic(error)
	}
	return string(bytes)
}

func VerifyPassword(userPassword string, providedPassword string) (string, bool) {
	// Compare Users Password and Provided Password
	err := bcrypt.CompareHashAndPassword([]byte(providedPassword), []byte(userPassword))
	check := true
	msg := ""

	if err != nil {
		msg = "login or password is incorrect"
		check = false
	}
	return msg, check
}

func DeleteUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		userId := c.Param("user_id")
		defer cancel()

		filter := bson.M{"user_id": userId}
		result, err := userCollection.DeleteOne(ctx, filter)
		res := map[string]interface{}{"data": result}

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"message": err})
			return
		}

		if result.DeletedCount < 1 {
			c.JSON(http.StatusInternalServerError, gin.H{"message": "No data to delete"})
			return
		}

		c.JSON(http.StatusCreated, gin.H{"message": "Table deleted successfully", "Data": res})
	}
}

func UpdateProfile(c *gin.Context, result string) {
	var ctx, cancel = context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	var user models.User

	if err := c.BindJSON(&user); err != nil {
		return
	}

	userId := c.Param("user_id")
	filter := bson.M{"user_id": userId}

	var updateObj primitive.D

	user.Image = &result

	if *user.Image != "" {
		updateObj = append(updateObj, bson.E{"image", user.Image})
	}

	user.Updated_at, _ = time.Parse(time.RFC3339, time.Now().Format(time.RFC3339))
	updateObj = append(updateObj, bson.E{"updated_at", user.Updated_at})

	upsert := true
	opt := options.UpdateOptions{
		Upsert: &upsert,
	}
	_, err := userCollection.UpdateOne(
		ctx,
		filter,
		bson.D{{"$set", updateObj}},
		&opt,
	)
	if err != nil {
		// msg := "User Failed"

	}

	c.JSON(http.StatusOK, gin.H{"message": "User Updated Successfully!"})

}
