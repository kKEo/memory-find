// The Objective-C half of package macwin. Every function copies its string
// arguments and returns at once; the AppKit work runs later on the main
// queue, so no Go caller ever waits for the main thread.

void macwin_init(int debug);
void macwin_open(const char *key, const char *title, const char *url);
void macwin_message(const char *key, const char *html);
