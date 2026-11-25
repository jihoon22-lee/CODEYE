#include <curl/curl.h>
#include <stdio.h>
#include <stdlib.h>

int main(void) {
    char url[512];
    CURL *h = curl_easy_init();
    if (h == NULL) {
        return 1;
    }
    char *id = curl_easy_escape(h, getenv("QUERY_STRING"), 0);
    snprintf(url, sizeof(url), "https://api.example.com/users/%s", id);
    curl_easy_setopt(h, CURLOPT_URL, url);
    curl_easy_perform(h);
    curl_free(id);
    curl_easy_setopt(h, CURLOPT_URL, "https://status.example.com/health");
    curl_easy_perform(h);
    curl_easy_cleanup(h);
    return 0;
}
