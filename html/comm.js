
function All(str) {
    return document.querySelectorAll(str)
}
/**
 * @return Element
 * @constructor
 */
function Q(str) {
    return document.querySelector(str)
}

/**
 * @return Element
 */
Element.prototype.Q = function (selectors) {
    return this.querySelector(selectors);
};

/**
 *
 * @param name
 * @param value
 * @returns {string | null}
 * @constructor
 */
Element.prototype.Data = function (name, value) {
    if (value === undefined) {
        return this.getAttribute("data-" + name);
    } else {
        this.setAttribute("data-" + name, value)
    }
};
/**
 * @returns EventTarget
 */
EventTarget.prototype.On = function () {
    this.addEventListener.apply(this, arguments);
    return this;
};