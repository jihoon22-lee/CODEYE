#include <curl/curl.h>
#include <stdlib.h>

int main(void) {
    CURL *h = curl_easy_init();
    if (h == NULL) {
        return 1;
    }
    const char *target = getenv("HTTP_X_FORWARD_TO");
    curl_easy_setopt(h, CURLOPT_URL, target); /* expect: codeye.c.ssrf */
    curl_easy_perform(h);
    curl_easy_cleanup(h);
    return 0;
}
