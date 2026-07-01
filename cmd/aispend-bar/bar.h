#ifndef AISPEND_BAR_H
#define AISPEND_BAR_H

// Implemented in bar_darwin.m, called from Go (main.go). SetTitle/SetHTML/QuitBar all
// marshal onto the main queue, so Go may call them from any goroutine.
void RunBar(void);             // build the status item + popover, then [NSApp run] (blocks)
void SetTitle(const char *s);  // set the menu-bar title
void SetHTML(const char *s);   // load HTML into the popover's web view
void QuitBar(void);            // terminate the app

#endif
