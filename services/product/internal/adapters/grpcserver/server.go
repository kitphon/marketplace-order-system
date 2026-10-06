package grpcserver

import (
	"context"
	"errors"

	productv1 "github.com/kitphon/marketplace-order-system/contracts/product/v1"
	"github.com/kitphon/marketplace-order-system/services/product/internal/application"
	"github.com/kitphon/marketplace-order-system/services/product/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	productv1.UnimplementedProductServiceServer
	getProducts *application.GetProducts
}

func New(getProducts *application.GetProducts) *Server {
	return &Server{getProducts: getProducts}
}

func (server *Server) GetProducts(
	ctx context.Context,
	request *productv1.GetProductsRequest,
) (*productv1.GetProductsResponse, error) {
	var productIDs []string
	if request != nil {
		productIDs = request.GetProductIds()
	}
	products, err := server.getProducts.Execute(ctx, productIDs)
	if err != nil {
		if errors.Is(err, application.ErrInvalidGetProductsRequest) {
			return nil, status.Error(codes.InvalidArgument, "product_ids must be non-empty and unique")
		}
		return nil, status.Error(codes.Internal, "get products failed")
	}

	response := &productv1.GetProductsResponse{Products: make([]*productv1.Product, 0, len(products))}
	for _, product := range products {
		response.Products = append(response.Products, productResponse(product))
	}
	return response, nil
}

func productResponse(product domain.Product) *productv1.Product {
	return &productv1.Product{
		ProductId:      product.ProductID(),
		SellerId:       product.SellerID(),
		Name:           product.Name(),
		UnitPriceMinor: product.UnitPriceMinor(),
		Currency:       product.Currency(),
		Active:         product.Active(),
	}
}
