cmake_minimum_required(VERSION 3.20)
project(w16 VERSION 1.0 LANGUAGES C CXX)

set(CMAKE_EXPORT_COMPILE_COMMANDS ON)
set(CMAKE_C_STANDARD 11)
set(CMAKE_CXX_STANDARD 17)

configure_file(config.h.in ${CMAKE_BINARY_DIR}/gen/config.h)

add_library(w16 STATIC src/ring.c src/parse.cpp src/util.cpp)
add_library(w16_shared SHARED src/ring.c)
target_include_directories(w16 PUBLIC include ${CMAKE_BINARY_DIR}/gen)
target_include_directories(w16_shared PUBLIC include ${CMAKE_BINARY_DIR}/gen)
target_compile_definitions(w16 PRIVATE RING_MAX=8)
target_compile_definitions(w16_shared PRIVATE RING_MAX=8 RING_SHARED)
